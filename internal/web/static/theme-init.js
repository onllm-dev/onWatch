// Theme initialization - runs synchronously to prevent flash of unstyled content
(function() {
    const saved = localStorage.getItem('onwatch-theme');
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const theme = saved || (prefersDark ? 'dark' : 'light');
    document.documentElement.setAttribute('data-theme', theme);
})();
