// Peak/off-peak helpers shared by the dashboard and the menubar popover.
// DeepSeek's published schedule is peak 01:00-04:00 and 06:00-10:00 UTC,
// Monday through Friday; every other hour, including the whole weekend UTC, is
// off-peak. Countdowns use milliseconds so the chip never jumps in whole days.
(() => {
  const HOUR_MS = 60 * 60 * 1000;

  const DEEPSEEK_PEAK_WINDOWS = [
    { startHour: 1, endHour: 4 },
    { startHour: 6, endHour: 10 },
  ];

  const DEEPSEEK_SCHEDULE_TEXT = "DeepSeek peak 01:00-04:00 and 06:00-10:00 UTC, Mon-Fri; off-peak all other hours";

  // peakStateAt evaluates the schedule at a Date. Windows are half-open, so
  // exactly 01:00 is peak and exactly 04:00 is off-peak. The scan spans nine
  // UTC days so a Friday-evening or weekend instant finds the next Monday
  // window. Returns {peak, until: Date, remainingMs}.
  function peakStateAt(now) {
    const at = now instanceof Date ? now : new Date(now);
    const dayMs = 24 * HOUR_MS;
    const today = Date.UTC(at.getUTCFullYear(), at.getUTCMonth(), at.getUTCDate());
    let nextStart = null;
    for (let offset = 0; offset < 9; offset += 1) {
      const dayStart = today + offset * dayMs;
      const weekday = new Date(dayStart).getUTCDay();
      if (weekday < 1 || weekday > 5) continue;
      for (const window of DEEPSEEK_PEAK_WINDOWS) {
        const start = dayStart + window.startHour * HOUR_MS;
        const end = dayStart + window.endHour * HOUR_MS;
        if (at.getTime() >= start && at.getTime() < end) {
          return { peak: true, until: new Date(end), remainingMs: end - at.getTime() };
        }
        if (start > at.getTime() && nextStart === null) {
          nextStart = start;
        }
      }
    }
    if (nextStart === null) {
      return { peak: false, until: null, remainingMs: 0 };
    }
    return { peak: false, until: new Date(nextStart), remainingMs: nextStart - at.getTime() };
  }

  // peakCountdownText renders "2h 10m", "45m", or "39s". Sub-minute values are
  // shown in seconds so the final tick is honest rather than a stale "1m".
  function peakCountdownText(remainingMs) {
    if (!Number.isFinite(remainingMs) || remainingMs <= 0) return "";
    const totalSeconds = Math.ceil(remainingMs / 1000);
    if (totalSeconds < 60) return totalSeconds + "s";
    const totalMinutes = Math.floor(totalSeconds / 60);
    if (totalMinutes < 60) return totalMinutes + "m";
    const hours = Math.floor(totalMinutes / 60);
    const minutes = totalMinutes % 60;
    return minutes > 0 ? hours + "h " + minutes + "m" : hours + "h";
  }

  // peakLabel returns "Peak till 2h 10m" or "Off-peak till 17h 50m" plus the
  // state the chip styles itself from.
  function peakLabel(now) {
    const state = peakStateAt(now);
    const countdown = peakCountdownText(state.remainingMs);
    const prefix = state.peak ? "Peak" : "Off-peak";
    return {
      peak: state.peak,
      until: state.until,
      remainingMs: state.remainingMs,
      text: countdown ? prefix + " till " + countdown : prefix,
    };
  }

  // peakTooltip joins the schedule, model note and pricing for the hover tip.
  function peakTooltip(meta) {
    const parts = [DEEPSEEK_SCHEDULE_TEXT];
    if (meta && meta.model_note) parts.push(meta.model_note);
    if (meta && meta.pricing) parts.push(meta.pricing);
    return parts.join("; ");
  }

  function escapePeakHTML(value) {
    return String(value == null ? "" : value).replace(/[&<>"']/g, (ch) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    }[ch]));
  }

  // peakChipMarkup builds the chip plus its custom tooltip. The wrapper is
  // focusable so keyboard users get the schedule too. The popover shows the
  // child tip on hover/focus; the dashboard hides it and shows a floating tip
  // instead because dashboard cards clip overflow.
  function peakChipMarkup(meta) {
    if (!meta) return "";
    const label = peakLabel(Date.now());
    const cls = label.peak ? "promo-peak" : "promo-offpeak";
    const provider = escapePeakHTML(meta.provider || "");
    const tip = peakTooltip(meta);
    return `<span class="peak-chip-wrap" data-peak-provider="${provider}" data-peak-tip="${escapePeakHTML(tip)}" tabindex="0" role="img" aria-label="${escapePeakHTML(label.text + ". " + tip)}">`
      + `<span class="peak-chip ${cls}" data-peak-label>${escapePeakHTML(label.text)}</span>`
      + `<span class="peak-chip-tip" role="tooltip" aria-hidden="true">${escapePeakHTML(tip)}</span>`
      + "</span>";
  }

  // refreshPeakChips updates every chip in place from the page's own clock, so
  // the 30s ticker needs no refetch. The schedule is the same for every chip.
  function refreshPeakChips(root) {
    const scope = root || document;
    scope.querySelectorAll(".peak-chip[data-peak-label]").forEach((el) => {
      const label = peakLabel(Date.now());
      el.className = "peak-chip " + (label.peak ? "promo-peak" : "promo-offpeak");
      el.textContent = label.text;
    });
  }

  // Floating tooltip shared by every chip. Native title tooltips are not used:
  // they are unreliable in the macOS WKWebView panel, and card overflow would
  // clip a child-node tip.
  let floatingTip = null;
  let activeWrap = null;

  function ensureFloatingTip() {
    if (floatingTip) return floatingTip;
    floatingTip = document.createElement("div");
    floatingTip.className = "peak-chip-floating-tip";
    floatingTip.setAttribute("role", "tooltip");
    floatingTip.style.display = "none";
    document.body.appendChild(floatingTip);
    return floatingTip;
  }

  function positionFloatingTip(wrap) {
    if (!floatingTip || !activeWrap) return;
    const rect = wrap.getBoundingClientRect();
    floatingTip.style.left = "8px";
    floatingTip.style.top = "8px";
    const tipRect = floatingTip.getBoundingClientRect();
    let left = rect.left;
    if (left + tipRect.width > window.innerWidth - 8) {
      left = Math.max(8, window.innerWidth - tipRect.width - 8);
    }
    let top = rect.top - tipRect.height - 6;
    if (top < 8) top = rect.bottom + 6;
    floatingTip.style.left = left + "px";
    floatingTip.style.top = top + "px";
  }

  function showFloatingTip(wrap) {
    const text = wrap.getAttribute("data-peak-tip");
    if (!text) return;
    activeWrap = wrap;
    ensureFloatingTip();
    floatingTip.textContent = text;
    floatingTip.style.display = "block";
    positionFloatingTip(wrap);
  }

  function hideFloatingTip() {
    activeWrap = null;
    if (floatingTip) floatingTip.style.display = "none";
  }

  function initPeakChipTooltips() {
    if (typeof document === "undefined" || typeof window === "undefined") return;
    if (window.__onWatchPeakTipsInit) return;
    window.__onWatchPeakTipsInit = true;

    const wrapFrom = (event) => {
      const target = event.target;
      if (!target || typeof target.closest !== "function") return null;
      return target.closest(".peak-chip-wrap");
    };
    document.addEventListener("mouseover", (event) => {
      const wrap = wrapFrom(event);
      if (wrap) showFloatingTip(wrap);
    });
    document.addEventListener("mouseout", (event) => {
      const wrap = wrapFrom(event);
      if (!wrap) return;
      const to = event.relatedTarget;
      if (to && typeof wrap.contains === "function" && wrap.contains(to)) return;
      hideFloatingTip();
    });
    document.addEventListener("focusin", (event) => {
      const wrap = wrapFrom(event);
      if (wrap) showFloatingTip(wrap);
    });
    document.addEventListener("focusout", (event) => {
      const wrap = wrapFrom(event);
      if (!wrap) return;
      const to = event.relatedTarget;
      if (to && typeof wrap.contains === "function" && wrap.contains(to)) return;
      hideFloatingTip();
    });
    window.addEventListener("scroll", hideFloatingTip, true);
    window.addEventListener("resize", hideFloatingTip);
  }

  const api = {
    peakStateAt,
    peakCountdownText,
    peakLabel,
    peakTooltip,
    peakChipMarkup,
    refreshPeakChips,
    initPeakChipTooltips,
    DEEPSEEK_SCHEDULE_TEXT,
  };

  if (typeof module !== "undefined" && module.exports) {
    module.exports = api;
  }
  if (typeof window !== "undefined") {
    window.OnWatchPeak = api;
  }
})();
