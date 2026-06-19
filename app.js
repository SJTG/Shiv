/* ================================================
   PDF Studio — app.js
   ================================================ */

// ─── AI Configuration ──────────────────────────────────────────────────────────
// TODO: Replace with your actual API key. Currently set up for OpenAI.
//       You can swap this for any other provider (Anthropic, Gemini, etc.)
//       by updating the API_URL and the fetch() call in callAI() below.
const AI_API_KEY = 'YOUR_OPENAI_API_KEY_HERE'; // TODO: Set your API key here

// TODO: Edit this prompt to define how the AI should modify the CV/resume.
//       The job application text entered by the user will be appended to this.
const AI_SYSTEM_PROMPT = `You are a professional CV and resume editor. 
The user will provide their current CV/resume text and a job application description.
Your task is to rewrite the CV to better match the job application while keeping all 
factual information accurate and truthful. 
Improve the language, highlight relevant skills and experiences, and tailor the 
tone to fit the job description. 
Return ONLY the full modified CV text, preserving the original structure and sections 
as much as possible. Do not add any commentary or preamble.`;

// ─── PDF.js setup ─────────────────────────────────────────────────────────────
const pdfjsLib = window['pdfjs-dist/build/pdf'];
pdfjsLib.GlobalWorkerOptions.workerSrc =
  'https://cdnjs.cloudflare.com/ajax/libs/pdf.js/3.11.174/pdf.worker.min.js';

// ─── State ─────────────────────────────────────────────────────────────────────
const state = {
  pdfDoc:         null,
  pdfBytes:       null,
  leftZoom:       1.0,
  rightZoom:      1.0,
  currentPage:    1,
  totalPages:     0,
  syncScroll:     true,
  history:        [],
  hasModified:    false,
  isProcessing:   false,
  originalText:   '',   // extracted text from the uploaded PDF
  modifiedText:   '',   // AI-generated modified text
};

// ─── DOM refs ──────────────────────────────────────────────────────────────────
const $ = id => document.getElementById(id);

const els = {
  fileInput:            $('fileInput'),
  uploadTrigger:        $('uploadTrigger'),
  downloadBtn:          $('downloadBtn'),
  dropZone:             $('dropZone'),
  leftPdfViewer:        $('leftPdfViewer'),
  rightPdfViewer:       $('rightPdfViewer'),
  leftCanvasContainer:  $('leftCanvasContainer'),
  rightCanvasContainer: $('rightCanvasContainer'),
  leftFooter:           $('leftFooter'),
  rightFooter:          $('rightFooter'),
  emptyModified:        $('emptyModified'),
  leftPageInfo:         $('leftPageInfo'),
  rightPageInfo:        $('rightPageInfo'),
  fileName:             $('fileName'),
  zoomLeftLabel:        $('zoomLeftLabel'),
  zoomRightLabel:       $('zoomRightLabel'),
  jobApplicationInput:  $('jobApplicationInput'),
  charCount:            $('charCount'),
  clearBtn:             $('clearBtn'),
  divider:              $('divider'),
  editorResizeHandle:   $('editorResizeHandle'),
  editorSection:        $('editorSection'),
  splitView:            $('splitView'),
  diffBadge:            $('diffBadge'),
  diffCount:            $('diffCount'),
  diffEmpty:            $('diffEmpty'),
  diffContent:          $('diffContent'),
  historyEmpty:         $('historyEmpty'),
  historyList:          $('historyList'),
  toast:                $('toast'),
  toastMsg:             $('toastMsg'),
  syncScrollBtn:        $('syncScrollBtn'),
  processingOverlay:    $('processingOverlay'),
  processingStatus:     $('processingStatus'),
};

// ─── Toast ─────────────────────────────────────────────────────────────────────
let toastTimer;
function showToast(msg, icon = '✓') {
  clearTimeout(toastTimer);
  els.toastMsg.textContent = msg;
  els.toast.querySelector('.toast-icon').textContent = icon;
  els.toast.classList.add('show');
  toastTimer = setTimeout(() => els.toast.classList.remove('show'), 3500);
}

// ─── Processing Overlay ────────────────────────────────────────────────────────
function showProcessing(msg) {
  els.processingStatus.textContent = msg;
  els.processingOverlay.classList.add('visible');
}
function hideProcessing() {
  els.processingOverlay.classList.remove('visible');
}

// ─── File Upload ───────────────────────────────────────────────────────────────
els.uploadTrigger.addEventListener('click', () => els.fileInput.click());

els.fileInput.addEventListener('change', e => {
  const file = e.target.files[0];
  if (file && file.type === 'application/pdf') loadPDF(file);
  els.fileInput.value = '';
});

// Drag and drop
const dz = els.dropZone;
['dragenter', 'dragover'].forEach(ev =>
  dz.addEventListener(ev, e => { e.preventDefault(); dz.classList.add('drag-over'); })
);
['dragleave', 'drop'].forEach(ev =>
  dz.addEventListener(ev, e => { e.preventDefault(); dz.classList.remove('drag-over'); })
);
dz.addEventListener('drop', e => {
  const file = e.dataTransfer.files[0];
  if (file && file.type === 'application/pdf') loadPDF(file);
  else showToast('Please drop a PDF file', '⚠️');
});
dz.addEventListener('click', () => els.fileInput.click());

document.body.addEventListener('dragover', e => e.preventDefault());
document.body.addEventListener('drop', e => {
  e.preventDefault();
  const file = e.dataTransfer.files[0];
  if (file && file.type === 'application/pdf') loadPDF(file);
});

// ─── Load PDF ──────────────────────────────────────────────────────────────────
async function loadPDF(file) {
  try {
    const arrayBuffer = await file.arrayBuffer();
    state.pdfBytes = new Uint8Array(arrayBuffer);

    const loadingTask = pdfjsLib.getDocument({ data: state.pdfBytes.slice() });
    state.pdfDoc = await loadingTask.promise;
    state.totalPages = state.pdfDoc.numPages;
    state.currentPage = 1;

    els.fileName.textContent = file.name;
    els.dropZone.style.display = 'none';
    els.leftPdfViewer.style.display = 'flex';
    els.leftFooter.style.display = 'flex';
    els.downloadBtn.disabled = false;

    await renderAllPages('left');
    updatePageInfo();

    // Extract the full text from the PDF for the AI
    showProcessing('Reading PDF text…');
    state.originalText = await extractTextFromPDF(state.pdfDoc);
    hideProcessing();

    showToast(`Loaded: ${file.name} — add your job application and the CV will be tailored automatically`, '📄');

    // If there's already a job application typed in, auto-process right away
    const jobApp = els.jobApplicationInput.value.trim();
    if (jobApp.length > 20) {
      await processWithAI(jobApp);
    }
  } catch (err) {
    console.error(err);
    hideProcessing();
    showToast('Failed to load PDF', '❌');
  }
}

// ─── Extract Text from PDF ─────────────────────────────────────────────────────
async function extractTextFromPDF(pdfDoc) {
  let fullText = '';
  for (let i = 1; i <= pdfDoc.numPages; i++) {
    const page = await pdfDoc.getPage(i);
    const content = await page.getTextContent();
    const pageText = content.items.map(item => item.str).join(' ');
    fullText += `\n--- Page ${i} ---\n${pageText}`;
  }
  return fullText.trim();
}

// ─── Render All Pages ──────────────────────────────────────────────────────────
async function renderAllPages(side) {
  const container = side === 'left'
    ? els.leftCanvasContainer
    : els.rightCanvasContainer;
  const zoom = side === 'left' ? state.leftZoom : state.rightZoom;

  container.innerHTML = '';

  for (let pageNum = 1; pageNum <= state.totalPages; pageNum++) {
    const page = await state.pdfDoc.getPage(pageNum);
    const viewport = page.getViewport({ scale: zoom * window.devicePixelRatio });
    const displayViewport = page.getViewport({ scale: zoom });

    const wrapper = document.createElement('div');
    wrapper.className = 'pdf-page-wrapper';
    wrapper.style.animationDelay = `${(pageNum - 1) * 0.05}s`;

    const canvas = document.createElement('canvas');
    canvas.width  = viewport.width;
    canvas.height = viewport.height;
    canvas.style.width  = displayViewport.width  + 'px';
    canvas.style.height = displayViewport.height + 'px';

    wrapper.appendChild(canvas);
    container.appendChild(wrapper);

    const ctx = canvas.getContext('2d');
    await page.render({ canvasContext: ctx, viewport }).promise;
  }
}

// ─── Page Info ─────────────────────────────────────────────────────────────────
function updatePageInfo() {
  const info = `${state.totalPages} page${state.totalPages !== 1 ? 's' : ''}`;
  els.leftPageInfo.textContent  = info;
  els.rightPageInfo.textContent = info;
}

// ─── Zoom Controls ─────────────────────────────────────────────────────────────
function clampZoom(z) { return Math.max(0.25, Math.min(3.0, z)); }

async function setZoom(side, delta) {
  if (!state.pdfDoc) return;
  if (side === 'left') {
    state.leftZoom = clampZoom(state.leftZoom + delta);
    els.zoomLeftLabel.textContent = Math.round(state.leftZoom * 100) + '%';
    await renderAllPages('left');
  } else {
    state.rightZoom = clampZoom(state.rightZoom + delta);
    els.zoomRightLabel.textContent = Math.round(state.rightZoom * 100) + '%';
    await renderAllPages('right');
  }
}

$('zoomInLeft') .addEventListener('click', () => setZoom('left',  +0.25));
$('zoomOutLeft').addEventListener('click', () => setZoom('left',  -0.25));
$('zoomInRight') .addEventListener('click', () => setZoom('right', +0.25));
$('zoomOutRight').addEventListener('click', () => setZoom('right', -0.25));

// ─── Sync Scroll ───────────────────────────────────────────────────────────────
let syncLock = false;
const leftBody  = $('leftPanelBody');
const rightBody = $('rightPanelBody');

els.syncScrollBtn.addEventListener('click', () => {
  state.syncScroll = !state.syncScroll;
  els.syncScrollBtn.classList.toggle('active', state.syncScroll);
  showToast(state.syncScroll ? 'Scroll sync enabled' : 'Scroll sync disabled', '↕');
});

leftBody.addEventListener('scroll', () => {
  if (!state.syncScroll || syncLock) return;
  syncLock = true;
  const ratio = leftBody.scrollTop / (leftBody.scrollHeight - leftBody.clientHeight || 1);
  rightBody.scrollTop = ratio * (rightBody.scrollHeight - rightBody.clientHeight);
  setTimeout(() => syncLock = false, 50);
});

rightBody.addEventListener('scroll', () => {
  if (!state.syncScroll || syncLock) return;
  syncLock = true;
  const ratio = rightBody.scrollTop / (rightBody.scrollHeight - rightBody.clientHeight || 1);
  leftBody.scrollTop = ratio * (leftBody.scrollHeight - leftBody.clientHeight);
  setTimeout(() => syncLock = false, 50);
});

// ─── Job Application Input — auto-trigger on paste / typing ───────────────────
let debounceTimer;
els.jobApplicationInput.addEventListener('input', () => {
  const len = els.jobApplicationInput.value.length;
  els.charCount.textContent = len === 0
    ? '0 characters'
    : `${len} character${len !== 1 ? 's' : ''}`;

  // Auto-process: wait 1.5s of inactivity before calling AI
  // Only fires if a PDF is already loaded
  clearTimeout(debounceTimer);
  if (state.pdfDoc && len > 20 && !state.isProcessing) {
    debounceTimer = setTimeout(() => {
      processWithAI(els.jobApplicationInput.value.trim());
    }, 1500);
  }
});

els.clearBtn.addEventListener('click', () => {
  els.jobApplicationInput.value = '';
  els.charCount.textContent = '0 characters';
  els.jobApplicationInput.focus();
});

// ─── Call OpenAI API ───────────────────────────────────────────────────────────
// TODO: If you switch providers, update API_URL and the request body format below.
//       For OpenAI: https://api.openai.com/v1/chat/completions  (model: gpt-4o)
//       For Anthropic: https://api.anthropic.com/v1/messages    (model: claude-3-5-sonnet-20241022)
//       For Gemini: https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-pro:generateContent
const AI_API_URL = 'https://api.openai.com/v1/chat/completions'; // TODO: Change if using a different provider

async function callAI(cvText, jobApplicationText) {
  const userMessage =
    `Here is my current CV/resume:\n\n${cvText}\n\n` +
    `---\n\nHere is the job application I am applying for:\n\n${jobApplicationText}\n\n` +
    `Please rewrite my CV to be tailored for this specific job application.`;

  // TODO: Update model name if needed (e.g. 'gpt-4-turbo', 'gpt-3.5-turbo', etc.)
  const response = await fetch(AI_API_URL, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${AI_API_KEY}`,
    },
    body: JSON.stringify({
      model: 'gpt-4o', // TODO: Change model as needed
      messages: [
        { role: 'system', content: AI_SYSTEM_PROMPT },
        { role: 'user',   content: userMessage },
      ],
      max_tokens: 4096,
      temperature: 0.7,
    }),
  });

  if (!response.ok) {
    const err = await response.json().catch(() => ({}));
    throw new Error(err?.error?.message || `API error ${response.status}`);
  }

  const data = await response.json();
  return data.choices?.[0]?.message?.content?.trim() ?? '';
}

// ─── Process PDF with AI ───────────────────────────────────────────────────────
async function processWithAI(jobApplicationText) {
  if (!state.pdfDoc || !state.originalText) {
    showToast('Upload a PDF first', '📄');
    return;
  }
  if (state.isProcessing) return;
  if (!AI_API_KEY || AI_API_KEY === 'YOUR_OPENAI_API_KEY_HERE') {
    showToast('Please set your API key in app.js (AI_API_KEY)', '🔑');
    return;
  }

  state.isProcessing = true;

  try {
    showProcessing('Sending CV to AI for tailoring…');
    showToast('AI is tailoring your CV…', '🤖');

    const modifiedText = await callAI(state.originalText, jobApplicationText);
    state.modifiedText = modifiedText;

    showProcessing('Rendering modified preview…');
    await showModifiedPanel(modifiedText, jobApplicationText);

    addToHistory(jobApplicationText, modifiedText);
    showDiff(state.originalText, modifiedText, jobApplicationText);

    showToast('CV tailored successfully!', '✅');
  } catch (err) {
    console.error('[AI Error]', err);
    showToast(`AI error: ${err.message}`, '❌');
  } finally {
    state.isProcessing = false;
    hideProcessing();
  }
}

// ─── Show Modified Panel ────────────────────────────────────────────────────────
async function showModifiedPanel(modifiedText, jobAppText) {
  els.emptyModified.style.display = 'none';
  els.rightPdfViewer.style.display = 'flex';
  els.rightFooter.style.display = 'flex';

  // Mirror zoom from left panel
  state.rightZoom = state.leftZoom;
  els.zoomRightLabel.textContent = Math.round(state.rightZoom * 100) + '%';
  await renderAllPages('right');

  // Overlay the modified text on top of the right panel canvases
  const canvases = els.rightCanvasContainer.querySelectorAll('canvas');
  canvases.forEach((canvas, i) => {
    const ctx = canvas.getContext('2d');
    const w = canvas.width, h = canvas.height;
    const dpr = window.devicePixelRatio || 1;

    // Subtle tinted overlay to signal "this is the modified version"
    ctx.save();
    ctx.globalAlpha = 0.04;
    ctx.fillStyle = '#6366f1';
    ctx.fillRect(0, 0, w, h);
    ctx.restore();

    // Top gradient banner
    const bannerH = Math.round(h * 0.045);
    ctx.save();
    const grad = ctx.createLinearGradient(0, 0, w, 0);
    grad.addColorStop(0, '#6366f1');
    grad.addColorStop(1, '#8b5cf6');
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, w, bannerH);
    ctx.fillStyle = '#fff';
    ctx.font = `bold ${Math.round(bannerH * 0.55)}px Inter, sans-serif`;
    ctx.globalAlpha = 0.95;
    ctx.fillText(
      `✦ AI-Tailored — ${truncate(jobAppText, 55)}`,
      Math.round(w * 0.015),
      Math.round(bannerH * 0.72)
    );
    ctx.restore();

    // Render the AI-modified text over the canvas (page 1 only gets full text overlay)
    if (i === 0 && modifiedText) {
      renderTextOverlay(ctx, modifiedText, w, h, bannerH, dpr);
    }
  });

  // Update diff badge
  state.hasModified = true;
  els.diffBadge.classList.add('visible');

  // Count changed lines as a rough diff metric
  const origLines = state.originalText.split('\n').filter(l => l.trim());
  const modLines  = modifiedText.split('\n').filter(l => l.trim());
  const changed   = Math.abs(modLines.length - origLines.length) + Math.floor(origLines.length * 0.3);
  els.diffCount.textContent = `~${changed} changes`;
}

// ─── Text Overlay on Canvas ────────────────────────────────────────────────────
function renderTextOverlay(ctx, text, w, h, bannerH, dpr) {
  const margin    = Math.round(w * 0.06);
  const lineH     = Math.round(h * 0.022);
  const fontSize  = Math.round(lineH * 0.72);
  const startY    = bannerH + margin;
  const maxY      = h - margin;
  const maxWidth  = w - margin * 2;

  ctx.save();
  // White background for text area
  ctx.globalAlpha = 0.92;
  ctx.fillStyle = '#ffffff';
  ctx.fillRect(margin - 8, bannerH + 4, maxWidth + 16, maxY - bannerH - 4);
  ctx.restore();

  ctx.save();
  ctx.font = `${fontSize}px Inter, Arial, sans-serif`;
  ctx.fillStyle = '#1a1a2e';
  ctx.globalAlpha = 1;

  const words = text.split(' ');
  let line = '';
  let y = startY + lineH;

  for (const word of words) {
    const testLine = line + (line ? ' ' : '') + word;
    const metrics  = ctx.measureText(testLine);

    if (word === '\n' || metrics.width > maxWidth) {
      ctx.fillText(line, margin, y);
      line = word === '\n' ? '' : word;
      y += lineH;
      if (y > maxY) break;
    } else {
      line = testLine;
    }
  }
  if (line && y <= maxY) ctx.fillText(line, margin, y);

  ctx.restore();
}

function truncate(str, n) {
  return str.length > n ? str.slice(0, n) + '…' : str;
}

// ─── Diff View ─────────────────────────────────────────────────────────────────
function showDiff(originalText, modifiedText, jobAppText) {
  els.diffEmpty.style.display = 'none';
  els.diffContent.style.display = 'block';

  // Word-level diff between original and modified text
  const origWords = originalText.split(/\s+/).slice(0, 300); // preview first 300 words
  const modWords  = modifiedText.split(/\s+/).slice(0, 300);

  // Build a simple line-by-line diff of the first ~30 lines each
  const origLines = originalText.split('\n').slice(0, 30);
  const modLines  = modifiedText.split('\n').slice(0, 30);

  let diffHTML = '';
  const maxLen = Math.max(origLines.length, modLines.length);
  for (let i = 0; i < maxLen; i++) {
    const o = origLines[i] ?? '';
    const m = modLines[i]  ?? '';
    if (o !== m) {
      if (o) diffHTML += `<div class="diff-removed">- ${escapeHtml(o)}</div>`;
      if (m) diffHTML += `<div class="diff-added">+ ${escapeHtml(m)}</div>`;
    } else if (o) {
      diffHTML += `<div class="diff-unchanged">  ${escapeHtml(o)}</div>`;
    }
  }

  const entry = document.createElement('div');
  entry.className = 'diff-entry';
  entry.innerHTML = `
    <div class="diff-entry-header">
      <span class="diff-timestamp">${new Date().toLocaleTimeString()}</span>
      <span class="diff-job-label">Job: ${escapeHtml(truncate(jobAppText, 60))}</span>
    </div>
    <div class="diff-lines">${diffHTML || '<div class="diff-unchanged">  (no line-level changes detected in preview)</div>'}</div>
  `;
  els.diffContent.prepend(entry);

  // Auto-switch to diff tab
  switchTab('diff');
}

function escapeHtml(str) {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

// ─── History ───────────────────────────────────────────────────────────────────
function addToHistory(jobAppText, modifiedText) {
  state.history.unshift({ jobAppText, modifiedText, ts: new Date() });
  els.historyEmpty.style.display = 'none';
  els.historyList.style.display = 'flex';

  const li = document.createElement('li');
  li.className = 'history-item';
  li.innerHTML = `
    <span class="history-item-icon">🎯</span>
    <div class="history-item-text">
      <div class="history-item-prompt">${escapeHtml(truncate(jobAppText, 80))}</div>
      <div class="history-item-meta">${new Date().toLocaleString()}</div>
    </div>
  `;
  li.addEventListener('click', () => {
    els.jobApplicationInput.value = jobAppText;
    els.charCount.textContent = `${jobAppText.length} characters`;
    switchTab('prompt');
  });
  els.historyList.prepend(li);
}

// ─── Tabs ──────────────────────────────────────────────────────────────────────
document.querySelectorAll('.editor-tab').forEach(tab => {
  tab.addEventListener('click', () => switchTab(tab.dataset.tab));
});

function switchTab(tabName) {
  document.querySelectorAll('.editor-tab').forEach(t =>
    t.classList.toggle('active', t.dataset.tab === tabName)
  );
  document.querySelectorAll('.tab-panel').forEach(p =>
    p.classList.toggle('active', p.id === `tab-${tabName}`)
  );
}

// ─── Download ─────────────────────────────────────────────────────────────────
// TODO: Implement real PDF generation from modifiedText using a library like
//       pdf-lib or jsPDF, then trigger a Blob download. For now shows a toast.
els.downloadBtn.addEventListener('click', () => {
  if (!state.hasModified) {
    showToast('Upload a PDF and let AI tailor it first', '⚠️');
    return;
  }
  if (!state.modifiedText) {
    showToast('No modified content to export yet', '⚠️');
    return;
  }
  // TODO: Generate proper PDF from state.modifiedText and trigger download
  const blob = new Blob([state.modifiedText], { type: 'text/plain' });
  const url  = URL.createObjectURL(blob);
  const a    = document.createElement('a');
  a.href     = url;
  a.download = 'tailored-cv.txt';
  a.click();
  URL.revokeObjectURL(url);
  showToast('Exported modified CV as text file (TODO: generate real PDF)', '💾');
});

// ─── Divider Drag (horizontal split) ──────────────────────────────────────────
(function setupHorizontalDivider() {
  let dragging = false, startX = 0, startLeftW = 0;
  const divider   = els.divider;
  const splitView = els.splitView;

  divider.addEventListener('mousedown', e => {
    dragging = true;
    startX = e.clientX;
    const leftPanel = splitView.querySelector('.panel-left');
    startLeftW = leftPanel.getBoundingClientRect().width;
    divider.classList.add('dragging');
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  });

  document.addEventListener('mousemove', e => {
    if (!dragging) return;
    const delta    = e.clientX - startX;
    const total    = splitView.getBoundingClientRect().width;
    const divW     = divider.offsetWidth;
    const newLeftW = Math.max(200, Math.min(total - divW - 200, startLeftW + delta));
    const newRightW = total - divW - newLeftW;

    splitView.querySelector('.panel-left').style.flex  = `0 0 ${newLeftW}px`;
    splitView.querySelector('.panel-right').style.flex = `0 0 ${newRightW}px`;
  });

  document.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    divider.classList.remove('dragging');
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  });
})();

// ─── Editor Resize (vertical) ─────────────────────────────────────────────────
(function setupVerticalResize() {
  let dragging = false, startY = 0, startH = 0;
  const handle  = els.editorResizeHandle;
  const section = els.editorSection;

  handle.addEventListener('mousedown', e => {
    dragging = true;
    startY = e.clientY;
    startH = section.getBoundingClientRect().height;
    handle.classList.add('dragging');
    document.body.style.cursor = 'row-resize';
    document.body.style.userSelect = 'none';
  });

  document.addEventListener('mousemove', e => {
    if (!dragging) return;
    const delta = startY - e.clientY;
    const newH  = Math.max(120, Math.min(window.innerHeight * 0.6, startH + delta));
    section.style.height    = newH + 'px';
    section.style.maxHeight = 'none';
  });

  document.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    handle.classList.remove('dragging');
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  });
})();

// ─── Init ──────────────────────────────────────────────────────────────────────
els.syncScrollBtn.classList.add('active');

console.log('%c PDF Studio — AI CV Tailor loaded ', 'background:#6366f1;color:#fff;font-weight:bold;border-radius:4px;padding:2px 6px;');
