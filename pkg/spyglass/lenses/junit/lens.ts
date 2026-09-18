const addSectionExpanders = (): void => {
  const expanders = document.querySelectorAll<HTMLTableRowElement>('tr.section-expander');
  for (const expander of Array.from(expanders)) {
    expander.onclick = () => {
      const tbody = expander.parentElement.nextElementSibling;
      const icon = expander.querySelector('i')!;
      if (tbody.classList.contains('hidden-tests')) {
        tbody.classList.remove('hidden-tests');
        icon.innerText = 'expand_less';
      } else {
        tbody.classList.add('hidden-tests');
        icon.innerText = 'expand_more';
      }
      spyglass.contentUpdated();
    };
  }
};

const addTestExpanders = (): void => {
  const rows = document.querySelectorAll<HTMLTableRowElement>('.failure-name,.group-name,.flaky-name');
  for (const row of Array.from(rows)) {
    row.onclick = () => {
      const sibling = row.nextElementSibling;
      const icon = row.querySelector('i')!;
      if (sibling.classList.contains('hidden')) {
        sibling.classList.remove('hidden');
        icon.innerText = 'expand_less';
      } else {
        sibling.classList.add('hidden');
        icon.innerText = 'expand_more';
      }
      spyglass.contentUpdated();
    };
  }
};

const addStdoutStderrOpeners = (): void => {
  const links = document.querySelectorAll<HTMLAnchorElement>('a.open-stdout-stderr');
  for (const link of Array.from(links)) {
    link.onclick = (e) => {
      e.preventDefault();
      const text = (link.nextElementSibling as HTMLElement).innerHTML;
      const blob = new Blob([`
      <head>
        <meta charset="UTF-8">
        <title>Logs</title>
      </head>
      <body style="background-color: #303030; color: white; font-family: monospace; white-space: pre-wrap;">${text}</body>`], {type: 'text/html'});
      window.open(URL.createObjectURL(blob));
    };
  }
};

// Delegated to the container instead of one listener per icon: a run can have
// thousands of tests, so thousands of copy icons.
const addCopyTestNameButtons = (): void => {
  const container = document.getElementById('junit-container');
  if (!container) {
    return;
  }
  // Capture phase: the expand/collapse handlers in addTestExpanders are bound
  // directly to the row, which sits between the icon and this container, so a
  // bubble-phase listener here would run after the row's handler already
  // toggled it. Capturing runs top-down and lets us stop the click before it
  // gets there.
  container.addEventListener('click', (e) => {
    const button = (e.target as HTMLElement).closest<HTMLElement>('i.copy-test-name');
    if (!button) {
      return;
    }
    e.stopPropagation();
    navigator.clipboard.writeText(button.dataset.testName!).then(() => {
      button.innerText = 'check';
      setTimeout(() => {
        button.innerText = 'content_copy';
      }, 1000);
    }, () => {
      button.innerText = 'error_outline';
      setTimeout(() => {
        button.innerText = 'content_copy';
      }, 1000);
    });
  }, true);
};

const loaded = (): void => {
  addTestExpanders();
  addCopyTestNameButtons();
  addStdoutStderrOpeners();
  addSectionExpanders();
};

window.addEventListener('DOMContentLoaded', loaded);
