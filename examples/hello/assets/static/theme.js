(function () {
  try {
    var KEY = "docs-theme";
    var root = document.documentElement;

    function apply(t) {
      root.classList.toggle("theme-dark", t === "dark");
      root.classList.toggle("theme-light", t === "light");
    }

    apply(localStorage.getItem(KEY) || "dark");

    window.toggleTheme = function () {
      var next = root.classList.contains("theme-dark") ? "light" : "dark";
      apply(next);
      try { localStorage.setItem(KEY, next); } catch (e) {}
    };
  } catch (e) {}
})();
