// Theme store initialization - waits for Alpine to be ready
document.addEventListener('alpine:init', () => {
  Alpine.store('theme', {
    dark: localStorage.getItem('theme-dark') === 'true' || 
          (!localStorage.getItem('theme-dark') && window.matchMedia('(prefers-color-scheme: dark)').matches),
    
    init() {
      this.applyTheme();
      // Listen for system theme changes
      window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
        if (!localStorage.getItem('theme-dark')) {
          this.dark = e.matches;
          this.applyTheme();
        }
      });
    },
    
    toggle() {
      this.dark = !this.dark;
      localStorage.setItem('theme-dark', this.dark.toString());
      this.applyTheme();
    },
    
    applyTheme() {
      const html = document.documentElement;
      if (this.dark) {
        html.style.colorScheme = 'dark';
        html.setAttribute('data-theme', 'dark');
      } else {
        html.style.colorScheme = 'light';
        html.setAttribute('data-theme', 'light');
      }
    }
  });
});
