// Confirmation Modal Helper
function confirmModal(title, message) {
  return new Promise((resolve) => {
    const modalId = 'confirm-modal-' + Date.now();
    const container = document.createElement('div');
    container.id = modalId;
    container.innerHTML = `
      <dialog open x-data="{ open: true }" @keydown.escape="open = false; setTimeout(() => document.getElementById('${modalId}').remove(), 100)">
        <article>
          <header>
            <h3 x-text="'${title.replace(/'/g, "\\'")}'" style="margin: 0;"></h3>
            <button aria-label="Close" rel="prev" @click="open = false; setTimeout(() => document.getElementById('${modalId}').remove(), 100)"></button>
          </header>
          <p x-text="'${message.replace(/'/g, "\\'")}'"></p>
          <footer>
            <button type="button" class="secondary" @click="open = false; setTimeout(() => document.getElementById('${modalId}').remove(), 100)">Cancel</button>
            <button type="button" @click="open = false; setTimeout(() => { document.getElementById('${modalId}').remove(); }, 100); window.confirmResolve(true)">Confirm</button>
          </footer>
        </article>
      </dialog>
    `;
    document.body.appendChild(container);
    window.confirmResolve = resolve;
    Alpine.initTree(container);
    setTimeout(() => {
      const buttons = container.querySelectorAll('button');
      if (buttons.length > 1) buttons[1].focus();
    }, 100);
  });
}

// Toast Notification Helper
function showToast(message, duration = 3000, type = 'info') {
  const toastId = 'toast-' + Date.now();
  const container = document.createElement('div');
  container.id = toastId;
  container.innerHTML = `
    <div x-data="{ show: true, dismiss() { this.show = false; setTimeout(() => document.getElementById('${toastId}').remove(), 300); } }" 
         x-init="setTimeout(() => dismiss(), ${duration})"
         :style="{ opacity: show ? 1 : 0, transition: 'opacity 0.3s ease' }"
         style="position: fixed; bottom: 1rem; right: 1rem; z-index: 9999; max-width: 400px;">
      <article style="margin: 0; padding: 1rem; ${type === 'success' ? 'border-left: 4px solid #2fb344;' : type === 'error' ? 'border-left: 4px solid #d63939;' : 'border-left: 4px solid #0078d4;'} display: flex; justify-content: space-between; align-items: center; gap: 1rem;">
        <p style="margin: 0;" x-text="'${message.replace(/'/g, "\\'")}'"></p>
        <button type="button" class="secondary" style="padding: 0; min-height: auto; width: 1.5rem; height: 1.5rem; flex-shrink: 0;" @click="dismiss()" title="Close">×</button>
      </article>
    </div>
  `;
  document.body.appendChild(container);
  Alpine.initTree(container);
}

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
