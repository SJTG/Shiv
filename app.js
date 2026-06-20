/* ================================================
   PDF Studio — app.js
   ================================================ */

// ─── AI Configuration ──────────────────────────────────────────────────────────
// ia_api_key is loaded from config.js (git-ignored).
// To set your key: open config.js and paste your Gemini API key there.
// Provider: Google Gemini — https://ai.google.dev/

const AI_SYSTEM_PROMPT = `You are a professional CV editor. You will receive a structured JSON representing a CV and a job description. Rewrite ONLY the text values to better match the job description. Rules: do NOT rename, add, or remove any keys or array items. Do NOT change structure or nesting. Keep all facts unchanged. Return ONLY valid minified JSON with no commentary, no markdown fences, no preamble.`;

// ─── PDF.js setup ─────────────────────────────────────────────────────────────
const pdfjsLib = window['pdfjs-dist/build/pdf'];
pdfjsLib.GlobalWorkerOptions.workerSrc =
  'https://cdnjs.cloudflare.com/ajax/libs/pdf.js/3.11.174/pdf.worker.min.js';

// ─── State ─────────────────────────────────────────────────────────────────────
const state = {
  pdfDoc: null,
  pdfBytes: null,
  leftZoom: 1.0,
  rightZoom: 1.0,
  currentPage: 1,
  totalPages: 0,
  syncScroll: true,
  history: [],
  hasModified: false,
  isProcessing: false,
  parsedCV: null, // structured JSON extracted from the uploaded PDF
  modifiedCV: null, // AI-modified structured JSON
};

// ─── DOM refs ──────────────────────────────────────────────────────────────────
const $ = id => document.getElementById(id);

const els = {
  fileInput: $('fileInput'),
  uploadTrigger: $('uploadTrigger'),
  downloadBtn: $('downloadBtn'),
  dropZone: $('dropZone'),
  leftPdfViewer: $('leftPdfViewer'),
  rightPdfViewer: $('rightPdfViewer'),
  leftCanvasContainer: $('leftCanvasContainer'),
  rightCanvasContainer: $('rightCanvasContainer'),
  leftFooter: $('leftFooter'),
  rightFooter: $('rightFooter'),
  emptyModified: $('emptyModified'),
  leftPageInfo: $('leftPageInfo'),
  rightPageInfo: $('rightPageInfo'),
  fileName: $('fileName'),
  zoomLeftLabel: $('zoomLeftLabel'),
  zoomRightLabel: $('zoomRightLabel'),
  jobApplicationInput: $('jobApplicationInput'),
  charCount: $('charCount'),
  clearBtn: $('clearBtn'),
  divider: $('divider'),
  editorResizeHandle: $('editorResizeHandle'),
  editorSection: $('editorSection'),
  splitView: $('splitView'),
  diffBadge: $('diffBadge'),
  diffCount: $('diffCount'),
  diffEmpty: $('diffEmpty'),
  diffContent: $('diffContent'),
  historyEmpty: $('historyEmpty'),
  historyList: $('historyList'),
  toast: $('toast'),
  toastMsg: $('toastMsg'),
  syncScrollBtn: $('syncScrollBtn'),
  processingOverlay: $('processingOverlay'),
  processingStatus: $('processingStatus'),
  tailorBtn: $('tailorBtn'),
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

// ─── Load PDF ───────────────────────────────────────────────────────────�// ─── Deterministic PDF → Structured JSON Parser ─────────────────────────────────
async function parsePDFToJSON(pdfDoc) {
  // 1. Collect all text items across all pages
  const allItems = [];
  for (let p = 1; p <= pdfDoc.numPages; p++) {
    const page = await pdfDoc.getPage(p);
    const content = await page.getTextContent();
    for (const item of content.items) {
      if (!item.str) continue; // keep whitespace-only for spacing
      allItems.push({
        str: item.str,
        x: item.transform[4],
        y: item.transform[5],
        fontName: item.fontName || '',
        height: item.height || 0,
        page: p,
      });
    }
  }

  // 2. Sort by (page ASC, y DESC, x ASC)
  allItems.sort((a, b) => {
    if (a.page !== b.page) return a.page - b.page;
    if (Math.abs(a.y - b.y) > 2) return b.y - a.y;
    return a.x - b.x;
  });

  // 3. RECONSTRUCT FULL LINES — group items by (page, y-coordinate)
  //    This is critical because pdfjs splits text into tiny fragments.
  const lines = [];
  let currentLine = null;
  for (const item of allItems) {
    if (!currentLine ||
      currentLine.page !== item.page ||
      Math.abs(currentLine.y - item.y) > 2) {
      // Start new line
      currentLine = {
        page: item.page,
        y: item.y,
        maxHeight: item.height,
        parts: [item.str],
      };
      lines.push(currentLine);
    } else {
      // Same line — append
      currentLine.parts.push(item.str);
      currentLine.maxHeight = Math.max(currentLine.maxHeight, item.height);
    }
  }

  // Join parts into full text per line
  const textLines = lines.map(l => ({
    text: l.parts.join('').trim(),
    height: l.maxHeight,
    page: l.page,
  })).filter(l => l.text);

  console.log('[PDF Parser] Reconstructed lines:', textLines.map(l => l.text));

  // 4. Build the structured CV object
  const cv = {
    name: '',
    contact: { location: '', email: '', phone: '', portfolio: '', linkedin: '' },
    sections: {
      about_me: { content: '' },
      education: { institution: '', degree: '', coursework: [], period: '' },
      experience: [],
      skills: { technical: [], competencies: [], languages: [] },
    },
  };

  // Flexible section matching
  function detectSection(text) {
    const low = text.toLowerCase().trim();
    if (low === 'about me') return 'about_me';
    if (low === 'education') return 'education';
    if (low === 'experience') return 'experience';
    if (/^skills/i.test(low) || low === 'skills & interests'
      || low === 'skills and interests' || low === 'skills') return 'skills';
    return null;
  }

  let currentSection = null;
  let currentEntry = null;
  let nameFound = false;
  let contactParsed = false;
  let skillsSubsection = 'technical'; // track which skills sub-array we're filling

  // Find largest font on page 1 for the name
  const page1Lines = textLines.filter(l => l.page === 1);
  const maxHeight = Math.max(...page1Lines.map(l => l.height), 0);

  for (let idx = 0; idx < textLines.length; idx++) {
    const line = textLines[idx];
    const text = line.text;
    const lower = text.toLowerCase();
    const height = line.height;

    // RULE 7: NAME — first line on page 1 with the largest font
    if (!nameFound && line.page === 1 && Math.abs(height - maxHeight) < 0.5) {
      cv.name = (cv.name ? cv.name + ' ' : '') + text;
      nameFound = true;
      continue;
    }

    // RULE 6: CONTACT LINE — everything between name and first section
    if (nameFound && !contactParsed) {
      const sec = detectSection(text);
      if (sec) {
        contactParsed = true;
        currentSection = sec;
        currentEntry = null;
        continue;
      }
      // Parse contact: split by bullet separators
      const parts = text.split(/\s*[•●‣]\s*/);
      for (const part of parts) {
        const p = part.trim();
        if (!p) continue;
        if (p.includes('@')) cv.contact.email = p;
        else if (/linkedin\.com/i.test(p) || /linkedin/i.test(p)) cv.contact.linkedin = p;
        else if (/github\.com/i.test(p) || /github/i.test(p)) cv.contact.portfolio = p;
        else if (/^\+?[\d\s\-().]{7,20}$/.test(p)) cv.contact.phone = p;
        else if (!cv.contact.location) cv.contact.location = p;
        else cv.contact.location += ', ' + p;
      }
      continue;
    }

    // RULE 1: SECTION DETECTION
    const sec = detectSection(text);
    if (sec) {
      contactParsed = true;
      currentSection = sec;
      currentEntry = null;
      skillsSubsection = 'technical';
      continue;
    }

    // RULE 2: BULLET DETECTION
    const isBullet = /^[●•]/.test(text);
    const bulletText = isBullet ? text.replace(/^[●•]\s*/, '') : '';

    // ── EXPERIENCE SECTION ──
    if (currentSection === 'experience') {
      if (isBullet) {
        if (currentEntry) currentEntry.bullets.push(bulletText);
        continue;
      }
      // RULE 3: new job title (larger font, not a bullet)
      if (height >= 10) {
        currentEntry = { title: text, modality: '', period: '', bullets: [] };
        cv.sections.experience.push(currentEntry);
        continue;
      }
      // RULE 4: MODALITY / PERIOD
      if (currentEntry) {
        if (/(Presencial|Remote|Híbrido|Hybrid)/i.test(text)) {
          currentEntry.modality = (currentEntry.modality ? currentEntry.modality + ' ' : '') + text;
        } else if (/\d{4}/.test(text) || /present/i.test(text)) {
          currentEntry.period = (currentEntry.period ? currentEntry.period + ' ' : '') + text;
        } else {
          currentEntry.title += ' ' + text;
        }
      }
      continue;
    }

    // ── SKILLS SECTION ──
    if (currentSection === 'skills') {
      // Detect subsection label
      if (/^Technical\s+Skills:?/i.test(text)) {
        skillsSubsection = 'technical';
        const rest = text.replace(/^Technical\s+Skills:?\s*/i, '');
        if (rest) cv.sections.skills.technical.push(...rest.split(/\s*\|\s*/).filter(Boolean));
        continue;
      }
      if (/^Core\s+Competencies:?/i.test(text)) {
        skillsSubsection = 'competencies';
        const rest = text.replace(/^Core\s+Competencies:?\s*/i, '');
        if (rest) cv.sections.skills.competencies.push(...rest.split(/\s*\|\s*/).filter(Boolean));
        continue;
      }
      if (/^Languages:?/i.test(text)) {
        skillsSubsection = 'languages';
        const rest = text.replace(/^Languages:?\s*/i, '');
        if (rest) cv.sections.skills.languages.push(...rest.split(/\s*\|\s*/).filter(Boolean));
        continue;
      }
      // Any other text in skills — append items split by | to current subsection
      if (isBullet) {
        cv.sections.skills[skillsSubsection].push(bulletText);
      } else if (text.includes('|')) {
        cv.sections.skills[skillsSubsection].push(...text.split(/\s*\|\s*/).filter(Boolean));
      } else {
        cv.sections.skills[skillsSubsection].push(text);
      }
      continue;
    }

    // ── ABOUT ME ──
    if (currentSection === 'about_me') {
      cv.sections.about_me.content += (cv.sections.about_me.content ? ' ' : '') + text;
      continue;
    }

    // ── EDUCATION ──
    if (currentSection === 'education') {
      // Period line: contains a year AND possibly "present"
      if (/\d{4}/.test(text) && (/present/i.test(text) || /–|—|-/.test(text))) {
        cv.sections.education.period = text;
        continue;
      }
      // Coursework heading
      if (/^(Relevant|Related)\s+Coursework/i.test(text)) {
        const rest = text.replace(/^(Relevant|Related)\s+Coursework:?\s*/i, '');
        if (rest) cv.sections.education.coursework = rest.split(/\s*[,|]\s*/).filter(Boolean);
        continue;
      }
      // Degree line: contains typical degree keywords
      if (/bachelor|master|licenciatura|ingenier|associate|b\.?s\.?|m\.?s\.?|ph\.?d|degree/i.test(text)) {
        cv.sections.education.degree = text.replace(/^[-–—]\s*/, '').trim();
        continue;
      }
      // Institution (first non-matched text)
      if (!cv.sections.education.institution) {
        cv.sections.education.institution = text;
        continue;
      }
      // If degree not yet set and this is a short line, treat as degree
      if (!cv.sections.education.degree) {
        cv.sections.education.degree = text.replace(/^[-–—]\s*/, '').trim();
        continue;
      }
      // Otherwise coursework
      cv.sections.education.coursework.push(...text.split(/\s*[,|]\s*/).filter(Boolean));
      continue;
    }
  }

  return cv;
} iod = (currentEntry.period ? currentEntry.period + ' ' : '') + trimmed;
      } else {
  // Append to title or modality as metadata
  currentEntry.title += ' ' + trimmed;
}
continue;
    }

// ABOUT ME
if (currentSection === 'about_me') {
  cv.sections.about_me.content += (cv.sections.about_me.content ? ' ' : '') + trimmed;
  continue;
}

// EDUCATION
if (currentSection === 'education') {
  if (/^(Relevant|Related)\s+Coursework/i.test(trimmed)) {
    const rest = trimmed.replace(/^(Relevant|Related)\s+Coursework:?\s*/i, '');
    if (rest) cv.sections.education.coursework = rest.split(/\s*[,|]\s*/).filter(Boolean);
  } else if (/\d{4}/.test(trimmed)) {
    cv.sections.education.period = trimmed;
  } else if (!cv.sections.education.institution) {
    cv.sections.education.institution = trimmed;
  } else if (!cv.sections.education.degree) {
    cv.sections.education.degree = trimmed;
  } else {
    // Additional coursework items
    cv.sections.education.coursework.push(...trimmed.split(/\s*[,|]\s*/).filter(Boolean));
  }
  continue;
}
  }

return cv;
}

// Helper: flatten parsed CV JSON back to readable text for display
function cvJsonToText(cv) {
  if (!cv) return '';
  const lines = [];
  lines.push(cv.name || '');
  const c = cv.contact;
  const contactParts = [c.location, c.email, c.phone, c.portfolio, c.linkedin].filter(Boolean);
  if (contactParts.length) lines.push(contactParts.join(' • '));
  lines.push('');

  if (cv.sections.about_me?.content) {
    lines.push('ABOUT ME');
    lines.push(cv.sections.about_me.content);
    lines.push('');
  }

  const edu = cv.sections.education;
  if (edu?.institution) {
    lines.push('EDUCATION');
    lines.push(`${edu.institution}  ${edu.period || ''}`.trim());
    if (edu.degree) lines.push(edu.degree);
    if (edu.coursework?.length) lines.push('Coursework: ' + edu.coursework.join(', '));
    lines.push('');
  }

  if (cv.sections.experience?.length) {
    lines.push('EXPERIENCE');
    for (const exp of cv.sections.experience) {
      lines.push(`${exp.title}  ${exp.modality}  ${exp.period}`.trim());
      for (const b of exp.bullets) lines.push(`  • ${b}`);
    }
    lines.push('');
  }

  const sk = cv.sections.skills;
  if (sk?.technical?.length || sk?.competencies?.length || sk?.languages?.length) {
    lines.push('SKILLS & INTERESTS');
    if (sk.technical.length) lines.push('Technical: ' + sk.technical.join(' | '));
    if (sk.competencies.length) lines.push('Competencies: ' + sk.competencies.join(' | '));
    if (sk.languages.length) lines.push('Languages: ' + sk.languages.join(' | '));
  }

  return lines.join('\n');
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
    canvas.width = viewport.width;
    canvas.height = viewport.height;
    canvas.style.width = displayViewport.width + 'px';
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
  els.leftPageInfo.textContent = info;
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

$('zoomInLeft').addEventListener('click', () => setZoom('left', +0.25));
$('zoomOutLeft').addEventListener('click', () => setZoom('left', -0.25));
$('zoomInRight').addEventListener('click', () => setZoom('right', +0.25));
$('zoomOutRight').addEventListener('click', () => setZoom('right', -0.25));

// ─── Sync Scroll ───────────────────────────────────────────────────────────────
let syncLock = false;
const leftBody = $('leftPanelBody');
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

// ─── Job Application Input — Manual Trigger ────────────────────────────────────
els.jobApplicationInput.addEventListener('input', () => {
  const len = els.jobApplicationInput.value.length;
  els.charCount.textContent = len === 0
    ? '0 characters'
    : `${len} character${len !== 1 ? 's' : ''}`;
});

els.tailorBtn.addEventListener('click', () => {
  const jobApp = els.jobApplicationInput.value.trim();
  if (jobApp.length > 20) {
    processWithAI(jobApp);
  } else {
    showToast('Please paste a job application first (min 20 chars)', '⚠️');
  }
});

els.clearBtn.addEventListener('click', () => {
  els.jobApplicationInput.value = '';
  els.charCount.textContent = '0 characters';
  els.jobApplicationInput.focus();
});

// ─── Call Gemini API ────────────────────────────────────────────────────────
// Provider: Google Gemini — generativelanguage.googleapis.com
const GEMINI_MODEL = 'gemini-flash-latest';

async function callAI(cvJSON, jobApplicationText) {
  const prompt =
    `${AI_SYSTEM_PROMPT}\n\n` +
    `CV JSON:\n${JSON.stringify(cvJSON)}\n\n` +
    `---\n\nJob description:\n${jobApplicationText}`;

  const endpoint =
    `https://generativelanguage.googleapis.com/v1beta/models/${GEMINI_MODEL}:generateContent?key=${ia_api_key}`;
  const response = await fetch(endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      contents: [{ parts: [{ text: prompt }] }],
      generationConfig: {
        temperature: 0.5,
        maxOutputTokens: 8192,
        responseMimeType: 'application/json',
      },
    }),
  });

  if (!response.ok) {
    const err = await response.json().catch(() => ({}));
    throw new Error(
      err?.error?.message || `Gemini API error ${response.status}`
    );
  }

  const data = await response.json();
  const raw = data.candidates?.[0]?.content?.parts?.[0]?.text?.trim() ?? '';

  // Strip markdown fences if Gemini wraps them anyway
  const cleaned = raw.replace(/^```(?:json)?\s*/i, '').replace(/\s*```$/i, '').trim();
  return JSON.parse(cleaned);
}

// ─── Process PDF with AI ───────────────────────────────────────────────────────
async function processWithAI(jobApplicationText) {
  if (!state.pdfDoc || !state.parsedCV) {
    showToast('Upload a PDF first', '📄');
    return;
  }
  if (state.isProcessing) return;
  if (!ia_api_key || ia_api_key === 'your_gemini_api_key_here') {
    showToast('Please set your Gemini API key in config.js (ia_api_key)', '🔑');
    return;
  }

  state.isProcessing = true;

  try {
    showProcessing('Sending structured CV to AI…');
    showToast('AI is tailoring your CV…', '🤖');

    const modifiedCV = await callAI(state.parsedCV, jobApplicationText);
    state.modifiedCV = modifiedCV;
    console.log('[AI] Modified CV JSON:', modifiedCV);

    // Convert JSON back to readable text for display
    const modifiedText = cvJsonToText(modifiedCV);
    const originalText = cvJsonToText(state.parsedCV);

    showProcessing('Rendering modified preview…');
    await showModifiedPanel(modifiedText, jobApplicationText);

    addToHistory(jobApplicationText, modifiedText);
    showDiff(originalText, modifiedText, jobApplicationText);

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

  // Count changed fields as a rough diff metric
  const origStr = JSON.stringify(state.parsedCV || {});
  const modStr = JSON.stringify(state.modifiedCV || {});
  let changed = 0;
  const origVals = origStr.match(/"[^"]+"/g) || [];
  const modVals = modStr.match(/"[^"]+"/g) || [];
  for (let i = 0; i < Math.max(origVals.length, modVals.length); i++) {
    if (origVals[i] !== modVals[i]) changed++;
  }
  els.diffCount.textContent = `~${changed} changes`;
}

// ─── Text Overlay on Canvas ────────────────────────────────────────────────────
function renderTextOverlay(ctx, text, w, h, bannerH, dpr) {
  const margin = Math.round(w * 0.06);
  const lineH = Math.round(h * 0.022);
  const fontSize = Math.round(lineH * 0.72);
  const startY = bannerH + margin;
  const maxY = h - margin;
  const maxWidth = w - margin * 2;

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
    const metrics = ctx.measureText(testLine);

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
  const modWords = modifiedText.split(/\s+/).slice(0, 300);

  // Build a simple line-by-line diff of the first ~30 lines each
  const origLines = originalText.split('\n').slice(0, 30);
  const modLines = modifiedText.split('\n').slice(0, 30);

  let diffHTML = '';
  const maxLen = Math.max(origLines.length, modLines.length);
  for (let i = 0; i < maxLen; i++) {
    const o = origLines[i] ?? '';
    const m = modLines[i] ?? '';
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

// ─── Download (pdf-lib) ────────────────────────────────────────────────────────
// Uses pdf-lib (loaded via CDN) to write the AI-tailored text into a real PDF.
els.downloadBtn.addEventListener('click', async () => {
  if (!state.hasModified) {
    showToast('Upload a PDF and let AI tailor it first', '⚠️');
    return;
  }
  if (!state.modifiedCV) {
    showToast('No modified content to export yet', '⚠️');
    return;
  }

  // Convert JSON CV to text for PDF rendering
  const exportText = cvJsonToText(state.modifiedCV);

  try {
    showProcessing('Generating PDF…');

    const { PDFDocument, rgb, StandardFonts } = PDFLib;

    const pdfDoc = await PDFDocument.create();
    const font = await pdfDoc.embedFont(StandardFonts.Helvetica);
    const bold = await pdfDoc.embedFont(StandardFonts.HelveticaBold);

    const PAGE_W = 595;   // A4 width  in pts
    const PAGE_H = 842;   // A4 height in pts
    const MARGIN = 50;
    const LINE_H = 14;
    const FONT_SZ = 11;
    const maxW = PAGE_W - MARGIN * 2;

    // Wrap text into lines that fit the page width
    function wrapLines(text) {
      const result = [];
      for (const raw of text.split('\n')) {
        const words = raw.split(' ');
        let line = '';
        for (const w of words) {
          const test = line ? `${line} ${w}` : w;
          const tw = font.widthOfTextAtSize(test, FONT_SZ);
          if (tw > maxW && line) {
            result.push(line);
            line = w;
          } else {
            line = test;
          }
        }
        result.push(line); // push last (or empty) line
      }
      return result;
    }

    const lines = wrapLines(exportText);
    let page = pdfDoc.addPage([PAGE_W, PAGE_H]);
    let y = PAGE_H - MARGIN;

    // Header banner on first page
    page.drawRectangle({
      x: 0, y: PAGE_H - 36,
      width: PAGE_W, height: 36,
      color: rgb(0.388, 0.4, 0.945), // #6366f1
    });
    page.drawText('AI-Tailored CV -- PDF Studio', {
      x: MARGIN, y: PAGE_H - 24,
      size: 13, font: bold,
      color: rgb(1, 1, 1),
    });
    y = PAGE_H - MARGIN - 24;

    for (const line of lines) {
      if (y < MARGIN + LINE_H) {
        // New page
        page = pdfDoc.addPage([PAGE_W, PAGE_H]);
        y = PAGE_H - MARGIN;
      }
      if (line.trim()) {
        // Strip any non-Latin1 chars that Helvetica can't render
        const safeLine = line.replace(/[^\x00-\xFF]/g, '');
        page.drawText(safeLine, {
          x: MARGIN, y,
          size: FONT_SZ, font,
          color: rgb(0.1, 0.1, 0.13),
        });
      }
      y -= LINE_H;
    }

    const pdfBytes = await pdfDoc.save();
    const blob = new Blob([pdfBytes], { type: 'application/pdf' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'tailored-cv.pdf';
    a.click();
    URL.revokeObjectURL(url);

    showToast('PDF downloaded! ✦ AI-Tailored CV', '💾');
  } catch (err) {
    console.error('[PDF Export Error]', err);
    showToast(`PDF export failed: ${err.message}`, '❌');
  } finally {
    hideProcessing();
  }
});

// ─── Divider Drag (horizontal split) ──────────────────────────────────────────
(function setupHorizontalDivider() {
  let dragging = false, startX = 0, startLeftW = 0;
  const divider = els.divider;
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
    const delta = e.clientX - startX;
    const total = splitView.getBoundingClientRect().width;
    const divW = divider.offsetWidth;
    const newLeftW = Math.max(200, Math.min(total - divW - 200, startLeftW + delta));
    const newRightW = total - divW - newLeftW;

    splitView.querySelector('.panel-left').style.flex = `0 0 ${newLeftW}px`;
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
  const handle = els.editorResizeHandle;
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
    const newH = Math.max(120, Math.min(window.innerHeight * 0.6, startH + delta));
    section.style.height = newH + 'px';
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
