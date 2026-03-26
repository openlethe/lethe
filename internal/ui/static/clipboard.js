// Copy to clipboard with HTTP-safe fallback.
// navigator.clipboard requires HTTPS or localhost; execCommand fallback works everywhere.
async function copyToClipboard(text) {
  let success = false;
  try {
    await navigator.clipboard.writeText(text);
    success = true;
  } catch {
    // Fallback for non-HTTPS contexts
    const el = document.createElement('textarea');
    el.value = text;
    el.style.cssText = 'position:fixed;top:0;left:0;opacity:0;';
    document.body.appendChild(el);
    el.select();
    try {
      success = document.execCommand('copy');
    } catch {
      success = false;
    }
    document.body.removeChild(el);
  }
  return success;
}

// Show brief "Copied!" feedback on an element.
function showCopiedFeedback(el) {
  if (!el) return;
  const orig = el.textContent;
  el.textContent = '✓ Copied';
  el.style.color = 'var(--green)';
  setTimeout(() => {
    el.textContent = orig;
    el.style.color = '';
  }, 1500);
}
