// Mejoras de la interfaz. Todo funciona sin JavaScript; esto agrega comodidad.
// Es un archivo aparte porque la CSP no permite scripts en línea.
(function () {
  "use strict";
  document.documentElement.classList.add("js");
  var reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text) n.textContent = text;
    return n;
  }

  // ---------- selects que se envían solos ----------
  document.addEventListener("change", function (ev) {
    var t = ev.target;
    if (t.matches && t.matches("select[data-autosubmit]") && t.form) t.form.submit();
  });

  // ---------- borrador de la oferta ----------
  // El texto pegado se guarda en este navegador: si algo falla (o la sesión
  // expira) no se pierde.
  function storage(fn) { try { return fn(window.sessionStorage); } catch (e) { return null; } }
  document.querySelectorAll("textarea[data-draft]").forEach(function (ta) {
    var key = "shiv-draft-" + ta.getAttribute("data-draft");
    if (!ta.value) {
      var saved = storage(function (s) { return s.getItem(key); });
      if (saved) ta.value = saved;
    }
    ta.addEventListener("input", function () {
      storage(function (s) { s.setItem(key, ta.value); });
    });
  });
  function clearDrafts(form) {
    form.querySelectorAll("textarea[data-draft]").forEach(function (ta) {
      storage(function (s) { s.removeItem("shiv-draft-" + ta.getAttribute("data-draft")); });
    });
  }

  // ---------- formularios async: botón con carga y error con X ----------
  function errorSlot(form) {
    var sel = form.getAttribute("data-error-in");
    var slot = sel && document.querySelector(sel);
    if (slot) return { parent: slot, before: null };
    var btn = form.querySelector("button[type=submit], button:not([type])");
    var actions = btn && btn.closest(".actions");
    if (actions && form.contains(actions)) return { parent: actions.parentNode, before: actions };
    return { parent: form.parentNode, before: form.nextSibling };
  }

  function clearError(form) {
    var box = form._shivError;
    if (box && box.parentNode) box.parentNode.removeChild(box);
    form._shivError = null;
  }

  function showError(form, err) {
    clearError(form);
    var box = el("div", "async-error");
    box.setAttribute("role", "alert");
    var x = el("span", "x-icon", "✕");
    x.setAttribute("aria-hidden", "true");
    var body = el("div", "async-error-body");
    body.appendChild(el("strong", "", err.heading || "No se pudo completar el pedido"));
    if (err.message) body.appendChild(el("p", "", err.message));
    if (err.action && err.action.href) {
      var a = el("a", "async-error-action", err.action.label || "Resolver");
      a.href = err.action.href;
      body.appendChild(a);
    }
    var specs = [];
    if (err.status) specs.push("HTTP " + err.status);
    if (err.detail) specs.push(err.detail);
    if (specs.length) {
      var det = el("details");
      det.appendChild(el("summary", "", "Especificaciones"));
      det.appendChild(el("pre", "", specs.join("\n")));
      body.appendChild(det);
    }
    var close = el("button", "async-error-close", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Cerrar el error");
    close.addEventListener("click", function () { clearError(form); });
    box.appendChild(x);
    box.appendChild(body);
    box.appendChild(close);
    var slot = errorSlot(form);
    slot.parent.insertBefore(box, slot.before);
    form._shivError = box;
  }

  function setLoading(form, btn, on) {
    form.classList.toggle("is-busy", on);
    form.setAttribute("aria-busy", on ? "true" : "false");
    form.querySelectorAll("button").forEach(function (b) { b.disabled = on; });
    if (!btn) return;
    if (on) {
      btn._shivLabel = btn.innerHTML;
      btn.classList.add("is-loading");
      btn.textContent = "";
      btn.appendChild(el("span", "spinner"));
      btn.appendChild(document.createTextNode(form.getAttribute("data-loading") || "Procesando…"));
    } else if (btn._shivLabel != null) {
      btn.classList.remove("is-loading");
      btn.innerHTML = btn._shivLabel;
    }
  }

  document.addEventListener("submit", function (ev) {
    var form = ev.target;
    if (!form.hasAttribute || !form.hasAttribute("data-async") || !window.fetch || !window.FormData) return;
    ev.preventDefault();
    if (form.classList.contains("is-busy")) return;
    if (form.checkValidity && !form.checkValidity()) { form.reportValidity(); return; }
    var btn = ev.submitter || form.querySelector("button[type=submit], button:not([type])");
    var data = new FormData(form);
    if (ev.submitter && ev.submitter.name) data.append(ev.submitter.name, ev.submitter.value);
    // Igual que el navegador: urlencoded salvo los formularios con archivos
    // (el servidor lee los primeros con ParseForm).
    var body = form.enctype === "multipart/form-data" ? data : new URLSearchParams(data);
    clearError(form);
    setLoading(form, btn, true);

    fetch(form.action, {
      method: "POST", body: body, credentials: "same-origin",
      headers: { "X-Shiv-Async": "1" },
    }).then(function (resp) {
      var type = resp.headers.get("Content-Type") || "";
      if (resp.ok && type.indexOf("text/html") === 0) {
        clearDrafts(form);
        if (resp.redirected) { window.location.assign(resp.url); return null; }
        // Páginas que son resultado de un POST (keywords, cambios): se muestran
        // tal cual, como haría el navegador.
        return resp.text().then(function (html) {
          document.open();
          document.write(html);
          document.close();
        });
      }
      if (type.indexOf("application/json") === 0) {
        return resp.json().then(function (err) {
          err.status = err.status || resp.status;
          throw err;
        });
      }
      throw { status: resp.status, heading: "El servidor respondió algo inesperado", message: "Reintentá en un momento." };
    }).catch(function (err) {
      if (err instanceof Error) {
        err = { heading: "No se pudo conectar con Shiv", message: "¿Sigue corriendo el servidor? Tu texto no se perdió.", detail: err.message };
      }
      setLoading(form, btn, false);
      showError(form, err);
    });
  });

  // ---------- API key: ✓ / ✗ y desplegable de modelos ----------
  function detect(key) {
    key = key.trim();
    if (!key) return "";
    if (key.indexOf("sk-ant-") === 0) return "Anthropic";
    if (key.indexOf("gsk") === 0) return "Groq";
    if (key.indexOf("sk") === 0) return "OpenAI";
    if (key.indexOf("AQ") === 0 || key.indexOf("AIza") === 0) return "Gemini";
    return null;
  }

  document.querySelectorAll("input[data-key-detect]").forEach(function (input) {
    var status = document.getElementById(input.getAttribute("data-key-detect"));
    var select = document.getElementById(input.getAttribute("data-models") || "");
    var field = select && select.closest("[data-model-field]");
    var provLabel = field && field.querySelector("[data-provider-label]");
    var original = select ? Array.prototype.map.call(select.options, function (o) { return [o.value, o.selected]; }) : [];
    var timer = null, ctrl = null, seq = 0;

    function setStatus(state, title) {
      if (!status) return;
      status.className = "key-status" + (state ? " " + state : "");
      status.textContent = state === "ok" ? "✓" : state === "bad" ? "✕" : "";
      status.title = title || "";
      status.setAttribute("aria-label", title || "");
    }
    function fillModels(models, chosen) {
      if (!select) return;
      select.textContent = "";
      models.forEach(function (m) {
        var o = el("option", "", m);
        o.value = m;
        if (m === chosen) o.selected = true;
        select.appendChild(o);
      });
    }
    function restore() {
      if (!select) return;
      select.textContent = "";
      original.forEach(function (p) {
        var o = el("option", "", p[0]);
        o.value = p[0];
        o.selected = p[1];
        select.appendChild(o);
      });
      if (field && field.hasAttribute("data-hide-empty")) field.hidden = original.length === 0;
    }

    function check() {
      var key = input.value.trim();
      var p = detect(key);
      if (ctrl) ctrl.abort();
      if (p === "") { setStatus("", ""); restore(); return; }
      if (p === null) {
        setStatus("bad", "No reconozco esa clave: tiene que empezar con AQ, sk-, sk-ant- o gsk.");
        restore();
        return;
      }
      setStatus("pending", "Verificando con " + p + "…");
      var my = ++seq;
      ctrl = window.AbortController ? new AbortController() : null;
      var body = new URLSearchParams();
      body.set("apikey", key);
      fetch("/login/verify", { method: "POST", body: body, signal: ctrl && ctrl.signal, credentials: "same-origin" })
        .then(function (r) { return r.json(); })
        .then(function (res) {
          if (my !== seq) return;
          if (!res.ok) { setStatus("bad", res.error || "La clave no es válida."); restore(); return; }
          setStatus("ok", "Clave de " + res.label + " verificada");
          fillModels(res.models || [], res.default);
          if (provLabel) provLabel.textContent = res.label;
          if (field) field.hidden = false;
        })
        .catch(function (e) {
          if (my !== seq || (e && e.name === "AbortError")) return;
          setStatus("bad", "No se pudo verificar la clave: " + (e && e.message ? e.message : "error de red"));
        });
    }

    input.addEventListener("input", function () {
      clearTimeout(timer);
      timer = setTimeout(check, 450);
    });
    if (input.value.trim()) check();
  });

  // ---------- placeholders que se escriben solos ----------
  // data-typewriter='["texto 1", "texto 2"]': escribe, espera, borra y pasa al
  // siguiente. Solo se ve mientras el campo está vacío.
  document.querySelectorAll("[data-typewriter]").forEach(function (input) {
    var words;
    try { words = JSON.parse(input.getAttribute("data-typewriter")); } catch (e) { return; }
    if (!words || !words.length) return;
    if (reduceMotion) { input.placeholder = words[0]; return; }
    var i = 0, n = 0, deleting = false;
    function tick() {
      var w = words[i];
      if (!deleting) {
        n++;
        input.placeholder = w.slice(0, n) + "▏";
        if (n >= w.length) { deleting = true; input.placeholder = w; return setTimeout(tick, 1600); }
        return setTimeout(tick, 70 + Math.random() * 60);
      }
      n--;
      input.placeholder = w.slice(0, n) + "▏";
      if (n <= 0) { deleting = false; i = (i + 1) % words.length; return setTimeout(tick, 350); }
      setTimeout(tick, 35);
    }
    input.placeholder = "";
    setTimeout(tick, 500);
  });

  // ---------- vista previa del CV ----------
  // Es una hoja A4: se escala para que entre entera de ancho en su columna.
  function fit(vp) {
    var frame = vp.querySelector("iframe");
    if (!frame) return;
    var page = parseFloat(vp.getAttribute("data-fit-page")) || 794;
    var w = vp.clientWidth, h = vp.clientHeight;
    if (!w || !h) return;
    var s = Math.min(1, w / page);
    frame.style.width = page + "px";
    frame.style.height = h / s + "px";
    frame.style.left = Math.max(0, (w - page * s) / 2) + "px";
    frame.style.transform = "scale(" + s + ")";
  }
  document.querySelectorAll("[data-fit-page]").forEach(function (vp) {
    fit(vp);
    if (window.ResizeObserver) new ResizeObserver(function () { fit(vp); }).observe(vp);
    else window.addEventListener("resize", function () { fit(vp); });
  });
})();
