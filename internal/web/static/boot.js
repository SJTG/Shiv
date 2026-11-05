// La animación de carga de Shiv se muestra una vez por pestaña: al navegar
// entre pantallas no vuelve a aparecer. Corre en el <head>, antes de pintar.
(function () {
  var root = document.documentElement;
  try {
    if (sessionStorage.getItem("shiv-intro") === "1") {
      root.classList.add("intro-seen");
      return;
    }
    sessionStorage.setItem("shiv-intro", "1");
  } catch (e) {
    // Sin storage la animación se muestra en cada carga; no rompe nada.
  }
  // La animación dura lo que dura aunque la página ya haya cargado; al terminar
  // se saca del árbol de accesibilidad y deja de capturar clics.
  document.addEventListener("DOMContentLoaded", function () {
    var loader = document.getElementById("shiv-loader");
    if (!loader) return;
    loader.addEventListener("animationend", function (ev) {
      if (ev.target === loader) loader.remove();
    });
  });
})();
