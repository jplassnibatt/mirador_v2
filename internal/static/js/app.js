(() => {
  "use strict";

  const csrf = document.querySelector('meta[name="csrf-token"]')?.content || "";
  const toastElement = document.getElementById("toast");
  const toastMessage = document.getElementById("toast-message");
  const toastClose = document.getElementById("toast-close");
  let toastTimer = null;
  let filterLock = false;

  function toast(message, isError = false) {
    if (!toastElement || !toastMessage) return;
    toastMessage.textContent = message;
    toastElement.classList.toggle("error", Boolean(isError));
    toastElement.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toastElement.classList.remove("show"), 3600);
  }

  // Dismiss the toast when the user clicks its close button.
  if (toastClose) {
    toastClose.addEventListener("click", () => {
      toastElement.classList.remove("show");
      clearTimeout(toastTimer);
    });
  }

  function escapeHtml(value) {
    const div = document.createElement("div");
    div.textContent = value;
    return div.innerHTML;
  }

  // renderLink turns a stored value into a safe external link. It supports the
  // markdown-style [text](url) pattern so users can store a display name with
  // the link, for example "[Google](https://www.google.com/?hl=es)". A plain
  // URL renders as <a href="url">url</a>. Non-URL text is shown as-is.
  function renderLink(value) {
    const s = String(value).trim();
    if (!s) return "—";
    const m = s.match(/^\[([^\]]*)\]\(([^)]*)\)$/);
    if (m) {
      return `<a href="${escapeHtml(m[2])}" target="_blank" rel="noopener noreferrer">${escapeHtml(m[1])}</a>`;
    }
    if (!s.startsWith("http://") && !s.startsWith("https://")) {
      return escapeHtml(s);
    }
    return `<a href="${escapeHtml(s)}" target="_blank" rel="noopener noreferrer">${escapeHtml(s)}</a>`;
  }

  // renderUrlFieldValue renders an account URL field in an edit dialog. CSM/AE
  // links show the matching name as the link text; other fields use renderLink.
  function renderUrlFieldValue(field, value, displayName) {
    if (!value) return "";
    if ((field === "csm_link" || field === "ae_link") && displayName) {
      return `<a href="${escapeHtml(value)}" target="_blank" rel="noopener noreferrer">${escapeHtml(displayName)}</a>`;
    }
    return renderLink(value);
  }

  // Sync the plain text of a contenteditable field back to its hidden input.
  function syncUrlField(div) {
    const field = div.dataset.field;
    const form = div.closest("form");
    if (!field || !form) return;
    const hidden = form.querySelector(`input[name="${field}"]`);
    if (!hidden) return;
    if (div.hasAttribute("data-rich")) {
      hidden.value = serializeRichField(div);
      return;
    }
    const link = div.querySelector("a");
    if (link) {
      const href = link.getAttribute("href") || link.textContent.trim();
      const text = link.textContent.trim();
      // CSM/AE links must stay bare URLs for server-side URL validation.
      // Other URL fields keep a custom display name as [text](url).
      if (field === "csm_link" || field === "ae_link") {
        hidden.value = href;
      } else if (text && text !== href) {
        hidden.value = `[${text}](${href})`;
      } else {
        hidden.value = href;
      }
    } else {
      const text = div.textContent.trim();
      hidden.value = text;
      // Auto-link plain URLs so they render as clickable hyperlinks.
      if (text && /^https?:\/\//.test(text)) {
        div.innerHTML = renderLink(text);
      }
    }
  }

  // Serialize contenteditable note content back to plain text. Links become
  // [text](url) like the account URL fields; line breaks are preserved.
  function serializeRichField(root) {
    let out = "";
    const walk = (node) => {
      if (node.nodeType === Node.TEXT_NODE) {
        out += node.nodeValue;
        return;
      }
      if (node.nodeType !== Node.ELEMENT_NODE) return;
      if (node.tagName === "BR") {
        out += "\n";
        return;
      }
      if (node.tagName === "A") {
        out += `[${node.textContent.trim()}](${node.getAttribute("href") || ""})`;
        return;
      }
      const block = node.tagName === "DIV" || node.tagName === "P" || node.tagName === "LI";
      if (block && out && !out.endsWith("\n")) out += "\n";
      node.childNodes.forEach(walk);
      if (block && !out.endsWith("\n")) out += "\n";
    };
    walk(root);
    return out.replace(/\u00a0/g, " ").replace(/\n{3,}/g, "\n\n").trim();
  }

  // Fill a contenteditable note field, rendering links so they are clickable
  // while editing (same approach as the account URL fields).
  function setRichField(div, value) {
    const text = String(value ?? "");
    div.innerHTML = text.trim() ? renderNote(text) : "";
  }

  // Fill a form's rich field (hidden input + contenteditable editor).
  function setFormRichField(form, name, value) {
    const text = String(value ?? "");
    if (form.elements[name]) form.elements[name].value = text;
    const field = form.querySelector(`[data-field="${name}"][data-rich]`);
    if (field) setRichField(field, text);
  }

  // Fill a form's notes field (hidden input + contenteditable editor).
  function setFormNotes(form, value) {
    setFormRichField(form, "notes", value);
  }

  // Insert a node at the caret inside a contenteditable field.
  function insertNodeAtCursor(node) {
    const selection = window.getSelection();
    if (!selection || !selection.rangeCount) return;
    const range = selection.getRangeAt(0);
    range.deleteContents();
    range.insertNode(node);
    range.setStartAfter(node);
    range.collapse(true);
    selection.removeAllRanges();
    selection.addRange(range);
  }

  // Sync all contenteditable URL fields to their hidden inputs before submit.
  function syncAllUrlFields(form) {
    form.querySelectorAll('[contenteditable="true"][data-field]').forEach(syncUrlField);
  }

  // escapeAttr escapes a value for use inside a double-quoted HTML attribute.
  function escapeAttr(value) {
    return escapeHtml(String(value ?? "")).replace(/"/g, "&quot;");
  }

  // renderNote escapes free-form note text and turns URLs into links using the
  // same representation as the account link fields: a plain http(s) URL or the
  // markdown-style [text](url). Line breaks are preserved.
  function renderNote(value) {
    const text = String(value ?? "").trim();
    if (!text) return "—";
    const linkify = (match, label, markdownUrl, plainUrl) => {
      const url = (markdownUrl || plainUrl || "").replace(/[.,;:!?]+$/, "");
      const href = url.replace(/"/g, "&quot;");
      const linkText = label !== undefined ? label : url;
      return `<a href="${href}" target="_blank" rel="noopener noreferrer">${linkText}</a>`;
    };
    return escapeHtml(text)
      .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)|(https?:\/\/[^\s<)]+)/g, linkify)
      .replace(/\n/g, "<br>");
  }

  // Sync contenteditable fields when they lose focus. Delegated so fields
  // rendered later (panel forms, task details) work too.
  document.addEventListener("focusout", (event) => {
    const div = event.target.closest?.('[contenteditable="true"][data-field]');
    if (div) syncUrlField(div);
  });

  // Pasting a plain URL into a rich note field inserts it as a link, matching
  // the account URL fields.
  document.addEventListener("paste", (event) => {
    const div = event.target.closest?.('[contenteditable="true"][data-rich]');
    if (!div) return;
    const text = event.clipboardData?.getData("text/plain")?.trim();
    if (!text || !/^https?:\/\/\S+$/.test(text)) return;
    // Rich pastes (a copied link with its label) are handled by the browser.
    if (event.clipboardData?.types?.includes("text/html")) return;
    event.preventDefault();
    const link = document.createElement("a");
    link.href = text;
    link.textContent = text;
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    insertNodeAtCursor(link);
    syncUrlField(div);
  });

  async function api(method, path, body) {
    const options = {
      method,
      headers: {
        "X-CSRF-Token": csrf,
        "Accept": "application/json",
      },
    };
    if (body !== undefined) {
      options.headers["Content-Type"] = "application/json";
      options.body = JSON.stringify(body);
    }
    const response = await fetch(path, options);
    const text = await response.text();
    let data = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = null;
      }
    }
    if (!response.ok) {
      throw new Error(data?.error || `Request failed (${response.status})`);
    }
    return data;
  }

  function formObject(form) {
    const data = {};
    for (const element of form.elements) {
      if (!element.name || element.disabled) continue;
      if (element.type === "checkbox" && element.name === "tag_ids") {
        if (!Array.isArray(data.tags)) data.tags = [];
      }
    }
    for (const element of form.elements) {
      if (!element.name || element.disabled) continue;
      if (element.type === "checkbox") {
        if (element.name === "tag_ids") {
          if (element.checked) data.tags.push({ id: Number(element.value) });
        } else {
          data[element.name] = element.checked;
        }
      } else if (element.type === "number") {
        if (element.value !== "") data[element.name] = Number(element.value);
      } else if (element.type === "file") {
        continue;
      } else {
        data[element.name] = element.value;
      }
    }
    delete data.id;
    return data;
  }

  function closeDialog(dialog) {
    if (dialog && typeof dialog.close === "function") dialog.close();
  }

  document.addEventListener("click", (event) => {
    const close = event.target.closest("[data-close-dialog]");
    if (close) {
      closeDialog(close.closest("dialog"));
      return;
    }
    const closeCategory = event.target.closest("[data-close-category]");
    if (closeCategory) {
      const details = closeCategory.closest("details");
      if (details) details.open = false;
      return;
    }
    const importButton = event.target.closest("[data-open-import]");
    if (importButton) {
      document.getElementById("import-dialog")?.showModal();
    }
    const exportButton = event.target.closest("[data-open-export]");
    if (exportButton) {
      document.getElementById("export-dialog")?.showModal();
    }
  });

  // New item dropdown menu.
  const newMenu = document.querySelector(".new-menu");
  const newForms = document.getElementById("new-forms");

  function closeNewMenu() {
    if (newMenu) newMenu.hidden = true;
    const toggle = document.querySelector("[data-new-menu]");
    if (toggle) toggle.classList.remove("open");
  }

  function showNewForm(kind) {
    if (!newForms) return;
    newForms.querySelectorAll("form").forEach((form) => {
      form.hidden = form.dataset.kind !== `${kind}-create`;
    });
  }

  document.addEventListener("click", (event) => {
    const newToggle = event.target.closest("[data-new-menu]");
    if (newToggle) {
      event.stopPropagation();
      const isOpen = newMenu.hidden;
      if (newMenu) newMenu.hidden = !newMenu.hidden;
      newToggle.classList.toggle("open", isOpen);
      return;
    }
    const newItem = event.target.closest("[data-new-form]");
    if (newItem) {
      closeNewMenu();
      showNewForm(newItem.dataset.newForm);
      return;
    }
    const closeNew = event.target.closest("[data-close-new-form]");
    if (closeNew) {
      const form = closeNew.closest("form");
      if (form) form.hidden = true;
      return;
    }
    if (!event.target.closest(".new-actions") && !event.target.closest(".new-forms")) {
      closeNewMenu();
    }
  });

  // Reload while keeping the currently open details panel after the reload.
  function reloadPreservingPanel() {
    // Save scroll position so the page doesn't jump to the top on reload.
    const main = document.querySelector("main.container");
    const splitLeft = document.querySelector(".split-left");
    const scrollState = {
      path: window.location.pathname + window.location.search,
      container: main ? main.scrollTop : 0,
      splitLeft: splitLeft ? splitLeft.scrollTop : 0,
    };
    try {
      sessionStorage.setItem("mirador.scroll", JSON.stringify(scrollState));
    } catch (error) {
      // Storage can fail in private modes; fall back to the plain reload.
    }

    try {
      if (panelView) {
        sessionStorage.setItem("mirador.panelView", JSON.stringify(panelView));
      }
    } catch (error) {
      // Storage can fail in private modes; fall back to the plain reload.
    }
    window.location.reload();
  }

  // Reload after a drag reorder without jumping back to the top of the page.
  // Also keeps the open details panel, since dragging an account or task while
  // its details are open must not close them.
  function reloadPreservingScroll() {
    reloadPreservingPanel();
  }

  // Restore the position saved by reloadPreservingScroll. Runs once per load
  // and is scoped to the same path so an unrelated page never inherits it.
  function restoreScrollPosition() {
    let state = null;
    try {
      state = JSON.parse(sessionStorage.getItem("mirador.scroll") || "null");
      sessionStorage.removeItem("mirador.scroll");
    } catch (error) {
      state = null;
    }
    if (!state || state.path !== window.location.pathname + window.location.search) return;
    const apply = () => {
      const main = document.querySelector("main.container");
      if (main && state.container) main.scrollTop = state.container;
      const splitLeft = document.querySelector(".split-left");
      if (splitLeft && state.splitLeft) splitLeft.scrollTop = state.splitLeft;
    };
    apply();
    // Re-apply after the first layout pass in case the browser resets it.
    requestAnimationFrame(() => requestAnimationFrame(apply));
  }

  restoreScrollPosition();

  document.addEventListener("submit", async (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    if (form.id === "import-form") return;
    if (!form.dataset.api) return;

    event.preventDefault();
    syncAllUrlFields(form);
    const method = form.dataset.method || "POST";
    let path = form.dataset.api;
    const kind = form.dataset.kind || "";

    try {
      const data = formObject(form);
      if (data.category_id !== undefined) data.category_id = Number(data.category_id);
      if (form.dataset.accountId) data.account_id = Number(form.dataset.accountId);
      if (data.churn_risk === "true") data.churn_risk = true;
      else if (data.churn_risk === "false") data.churn_risk = false;
      else if (data.churn_risk === "") data.churn_risk = null;

      if (kind === "category-edit" || kind === "account-edit" || kind === "task-edit" || kind === "tag-edit") {
        const id = form.elements.id?.value;
        if (!id) throw new Error("Missing record id");
        path += `/${id}`;
      }

      await api(method, path, data);
      closeDialog(form.closest("dialog"));
      toast("Saved");
      // When the task was edited from a details panel, refresh that view in
      // place instead of reloading back to the accounts list.
      if (kind === "task-edit" && panelView) {
        if (panelView.type === "task") {
          // On the Due date page a task edit can change its date group, so
          // reload the list and restore the same task panel afterwards.
          if (panelView.origin === "tasks") {
            reloadPreservingPanel();
            return;
          }
          openTaskDetails(panelView.taskId, panelView.accountId, panelView.origin);
        } else {
          openAccountDetails(panelView.accountId);
        }
        return;
      }

      // Editing an account can change its name or move it between categories,
      // so reload the list but restore the account details panel afterwards.
      if (kind === "account-edit" && panelView?.type === "account") {
        reloadPreservingPanel();
        return;
      }

      // Creating a task from the account details panel should return to that
      // same panel instead of dropping back to the accounts list.
      if (kind === "task-create" && panelView?.type === "account") {
        reloadPreservingPanel();
        return;
      }

      setTimeout(() => window.location.reload(), 180);
    } catch (error) {
      toast(error.message, true);
    }
  });

  // Close any open facet menu when clicking outside or pressing Escape.
  // Attached once; uses live queries so it stays correct after swaps.
  const closeFacetMenus = () => {
    document.querySelectorAll("details.facet-menu[open]").forEach((menu) => {
      menu.open = false;
    });
  };
  document.addEventListener("click", (event) => {
    if (!event.target.closest("details.facet-menu")) closeFacetMenus();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") closeFacetMenus();
  });

  // Filter bar: every control applies without a full page reload. The fetch
  // below swaps the filter bar and <main> with the server-rendered result, so
  // scroll position and focus are preserved.
  function initFilterForm(ff) {
    let searchTimer;
    let radioTimer;
    ff.addEventListener("input", (event) => {
      if (!event.target.matches("input[type='search']")) return;
      clearTimeout(searchTimer);
      searchTimer = setTimeout(() => ff.requestSubmit(), 300);
    });

    const facetMenus = Array.from(ff.querySelectorAll("details.facet-menu"));
    const openFacet = new URLSearchParams(window.location.search).get("open");
    if (openFacet) {
      facetMenus.find((menu) => menu.dataset.facet === openFacet)?.setAttribute("open", "");
    }
    facetMenus.forEach((menu) => {
      menu.addEventListener("toggle", () => {
        if (menu.open) {
          facetMenus.forEach((other) => {
            if (other !== menu) other.open = false;
          });
          return;
        }
        const url = new URL(window.location.href);
        if (url.searchParams.get("open") === menu.dataset.facet) {
          url.searchParams.delete("open");
          history.replaceState(null, "", url);
        }
      });
    });

    ff.addEventListener("change", (event) => {
      if (filterLock) return;
      if (event.target.matches("input[type='checkbox']")) {
        const menu = event.target.closest("details.facet-menu");
        if (menu) updateBadge(menu);
        ff.requestSubmit();
        return;
      }
      if (event.target.matches("input[type='radio']")) {
        // Arrowing through a radio group should not navigate per keypress.
        clearTimeout(radioTimer);
        radioTimer = setTimeout(() => ff.requestSubmit(), 400);
      }
    });

    // Radio buttons and the Show done checkbox live outside the filter form
    // (in the header), so listen at the document level for changes.
    // Use .closest() because clicking the label text targets the <label>,
    // not the <input> inside it.
    document.addEventListener("change", (event) => {
      if (filterLock) return;
      const radio = event.target.closest("input[name='status'][type='radio']");
      if (radio) {
        const url = new URL(window.location.href);
        url.searchParams.set("status", radio.value);
        saveScroll();
        applyFilter(url.toString(), window.scrollY);
        return;
      }
      const checkbox = event.target.closest("input[name='show_done'][type='checkbox']");
      if (checkbox) {
        const url = new URL(window.location.href);
        if (checkbox.checked) {
          url.searchParams.set("show_done", "1");
        } else {
          url.searchParams.delete("show_done");
        }
        saveScroll();
        applyFilter(url.toString(), window.scrollY);
        return;
      }
    });
    ff.addEventListener("click", (event) => {
      if (filterLock) return;
      const link = event.target.closest("a");
      if (!link) return;
      const href = new URL(link.href, window.location.href);
      if (href.origin === window.location.origin && href.pathname === window.location.pathname) {
        event.preventDefault();
        saveScroll();
        applyFilter(href.toString(), window.scrollY);
      }
    });
    ff.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (filterLock) return;
      clearTimeout(searchTimer);
      clearTimeout(radioTimer);
      saveScroll();
      const params = new URLSearchParams();
      for (const [key, raw] of new FormData(ff)) {
        const value = String(raw).trim();
        if (value) params.append(key, value);
      }
      const url = new URL(ff.action, window.location.href);
      url.search = params.toString();
      if (url.toString() === window.location.href) return;
      const scrollY = window.scrollY;
      await applyFilter(url.toString(), scrollY);
    });
  }

  // Update a facet menu's count badge from its checked checkboxes.
  function updateBadge(menu) {
    const summary = menu.querySelector("summary");
    const count = menu.querySelectorAll("input[type='checkbox']:checked").length;
    const existing = summary.querySelector(".facet-count");
    if (count > 0) {
      if (existing) existing.textContent = count;
      else summary.insertAdjacentHTML("beforeend", " <span class='facet-count'>" + count + "</span>");
    } else if (existing) {
      existing.remove();
    }
  }

  // Refresh all facet badge counts from current checkbox states.
  function refreshAllBadges() {
    document.querySelectorAll("details.facet-menu").forEach(updateBadge);
  }

  async function applyFilter(url, scrollY) {

    if (filterLock) return;
    filterLock = true;

    try {
      const res = await fetch(url, { headers: { "X-Requested-With": "Fetch" } });
      if (!res.ok) {
        window.location.href = url;
        return;
      }
      const html = await res.text();
      const doc = new DOMParser().parseFromString(html, "text/html");

      const newFilter = doc.querySelector("[data-filter-form]");
      const newMain = doc.querySelector("main");
      const oldFilter = document.querySelector("[data-filter-form]");
      const oldMain = document.querySelector("main");

      // Remember which facet menus are open before replacing innerHTML.
      const openMenus = Array.from(
        document.querySelectorAll("details.facet-menu[open]")
      ).map((m) => m.dataset.facet);

      // Save search input value and cursor position before replacement — the
      // element is recreated by innerHTML so its focus and selection are lost.
      const searchInput = oldFilter.querySelector("input[type='search']");
      const searchValue = searchInput ? searchInput.value : "";
      const searchStart = searchInput ? searchInput.selectionStart : 0;
      const searchEnd = searchInput ? searchInput.selectionEnd : 0;

      // Replace innerHTML of filter bar and <main> — server-rendered content
      // always has correct badge counts and checked states. Elements stay in
      // place so there's no focus loss or scroll jump.
      if (newFilter && oldFilter) oldFilter.innerHTML = newFilter.innerHTML;
      if (newMain && oldMain) oldMain.innerHTML = newMain.innerHTML;

      // Restore focus, value and cursor position to the search input so the
      // user can keep typing without the cursor jumping.
      const newSearchInput = oldFilter.querySelector("input[type='search']");
      if (newSearchInput) {
        newSearchInput.value = searchValue;
        newSearchInput.setSelectionRange(searchStart, searchEnd);
        newSearchInput.focus({ preventScroll: true });
      }

      // Guarantee badge counts are correct from current checkbox states.
      refreshAllBadges();

      // Strip the temporary `open=` param and push the clean URL.
      const cleanUrl = new URL(url);
      cleanUrl.searchParams.delete("open");
      history.pushState(null, "", cleanUrl.toString());

      // Update the page title for client-side navigations so screen readers
      // and browser tabs reflect the current view.
      const newTitle = doc.querySelector("title");
      if (newTitle) document.title = newTitle.textContent;

      // Reopen the facet menus that were open before the replacement.
      openMenus.forEach((facet) => {
        const menu = oldFilter.querySelector(`details.facet-menu[data-facet="${facet}"]`);
        if (menu) menu.setAttribute("open", "");
      });

      // Restore scroll aggressively: immediately, in the next frame, and on
      // timeout. Reopening menus above scrolls them into view asynchronously,
      // so we apply our scroll after the browser's layout.
      window.scrollTo(0, scrollY);
      requestAnimationFrame(() => window.scrollTo(0, scrollY));
      setTimeout(() => window.scrollTo(0, scrollY), 0);

      // If an account or task details panel was open before the swap,
      // re-open it so the panel content is restored after the <main>
      // innerHTML swap wiped it.
      // Re-init the splitter first so element references point to the new
      // <main> content (old ones were detached by the innerHTML swap).
      window.reinitSplitter?.();
      if (panelView && panelView.type === "account") {
        openAccountDetails(panelView.accountId);
      }
      if (panelView && panelView.type === "task") {
        openTaskDetails(panelView.taskId, panelView.accountId, panelView.origin);
      }

      initExpandButtons();
      syncDetailsAria();
      setupTaskDrag(oldMain);
      setupCategoryDrag(oldMain);
    } finally {
      filterLock = false;
    }
  }

  // Remember scroll position per URL so back/forward doesn't jump to top.
  const scrollPositions = new Map();
  const saveScroll = () => scrollPositions.set(window.location.href, window.scrollY);
  const restoreScroll = () => {
    const saved = scrollPositions.get(window.location.href);
    if (saved !== undefined) window.scrollTo(0, saved);
  };

  const filterForm = document.querySelector("[data-filter-form]");
  if (filterForm) initFilterForm(filterForm);

  window.addEventListener("popstate", async () => {
    restoreScroll();
    await applyFilter(window.location.href, window.scrollY);
  });

  // Expand/collapse category persists in the database. Re-runnable so it can
  // re-attach listeners after an in-place filter swap.
  function initExpandButtons() {
    document.querySelectorAll("[data-expand-category]").forEach((button) => {
      button.addEventListener("click", async () => {
        try {
          const expanded = button.dataset.expanded === "1";
          await api("PUT", `/api/v1/categories/${button.dataset.expandCategory}`, { expanded: !expanded });
          window.location.reload();
        } catch (error) {
          toast(error.message, true);
        }
      });
    });
  }
  initExpandButtons();

  // Sync aria-expanded on <summary> elements inside <details> so screen
  // readers know whether a disclosure (facet menu, add panel) is open.
  function syncDetailsAria() {
    document.querySelectorAll("details").forEach((details) => {
      const summary = details.querySelector("summary");
      if (summary) summary.setAttribute("aria-expanded", String(details.open));
    });
  }
  syncDetailsAria();

  // Complete/uncomplete. Delegated so tasks rendered later (e.g. inside the
  // account details panel) work too.
  document.addEventListener("change", async (event) => {
    const checkbox = event.target.closest("[data-toggle-task]");
    if (!checkbox) return;
    checkbox.disabled = true;
    try {
      await api("PUT", `/api/v1/tasks/${checkbox.dataset.toggleTask}`, { completed: checkbox.checked });
      // Reload while keeping the open details panel so the account details
      // view is not lost when toggling a task from inside the panel.
      reloadPreservingPanel();
    } catch (error) {
      checkbox.checked = !checkbox.checked;
      checkbox.disabled = false;
      toast(error.message, true);
    }
  });

  // Trash, restore, permanent delete. Delegated so rows rendered later
  // (e.g. tasks inside the account details panel) work too.
  document.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-delete]");
    if (!button) return;
    // Tags are deleted permanently; categories, accounts, and tasks go to
    // the trash first.
    const permanent = button.dataset.delete.startsWith("/api/v1/tags/");
    const message = permanent
      ? "Delete this tag permanently? Accounts using it will lose the tag."
      : "Archive this item?";
    if (!window.confirm(message)) return;
    try {
      await api("DELETE", button.dataset.delete);
      toast(permanent ? "Tag deleted" : "Archived");
      window.location.reload();
    } catch (error) {
      toast(error.message, true);
    }
  });

  document.querySelectorAll("[data-purge]").forEach((button) => {
    button.addEventListener("click", async () => {
      if (!window.confirm("Delete permanently? This cannot be undone.")) return;
      try {
        await api("DELETE", button.dataset.purge);
        toast("Deleted permanently");
        window.location.reload();
      } catch (error) {
        toast(error.message, true);
      }
    });
  });

  document.querySelectorAll("[data-restore-json]").forEach((button) => {
    button.addEventListener("click", async () => {
      try {
        const body = JSON.parse(button.dataset.restoreJson);
        await api("POST", "/api/v1/archives/restore", body);
        toast("Restored");
        window.location.reload();
      } catch (error) {
        toast(error.message, true);
      }
    });
  });

  // API keys: create a new key.
  const apiKeyForm = document.getElementById("api-key-form");
  if (apiKeyForm) {
    apiKeyForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const formData = new FormData(apiKeyForm);
      const name = formData.get("name");
      if (!name) return;
      try {
        const result = await api("POST", "/api/v1/api-keys", { name });
        const shown = document.getElementById("new-key-shown");
        const valueEl = document.getElementById("new-key-value");
        if (shown && valueEl) {
          valueEl.textContent = result.key;
          shown.hidden = false;
          shown.scrollIntoView({ behavior: "smooth" });
        }
        apiKeyForm.reset();
        toast("API key created — copy it now");
      } catch (error) {
        toast(error.message, true);
      }
    });
  }

  // API keys: revoke a key.
  document.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-delete-key]");
    if (!button) return;
    if (!window.confirm("Revoke this API key? It will stop working immediately.")) return;
    try {
      await api("DELETE", `/api/v1/api-keys/${button.dataset.id}`);
      toast("API key revoked");
      window.location.reload();
    } catch (error) {
      toast(error.message, true);
    }
  });

  // API keys: copy the revealed key to clipboard.
  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-copy-key]");
    if (!button) return;
    const valueEl = document.getElementById("new-key-value");
    if (!valueEl) return;
    navigator.clipboard.writeText(valueEl.textContent).then(() => {
      const old = button.textContent;
      button.textContent = "Copied!";
      setTimeout(() => { button.textContent = old; }, 1200);
    });
  });

  // Edit dialogs.
  document.querySelectorAll("[data-edit-category]").forEach((button) => {
    button.addEventListener("click", () => {
      const dialog = document.getElementById("category-dialog");
      const form = dialog.querySelector("form");
      form.elements.id.value = button.dataset.id;
      form.elements.name.value = button.dataset.name || "";
      form.elements.color.value = button.dataset.color || "#6b7280";
      form.elements.expanded.checked = button.dataset.expanded === "1";
      dialog.showModal();
    });
  });

  document.querySelectorAll("[data-edit-tag]").forEach((button) => {
    button.addEventListener("click", () => {
      const dialog = document.getElementById("tag-dialog");
      const form = dialog.querySelector("form");
      form.elements.id.value = button.dataset.id;
      form.elements.name.value = button.dataset.name || "";
      form.elements.color.value = button.dataset.color || "#6b7280";
      dialog.showModal();
    });
  });

  document.querySelectorAll("[data-edit-account]").forEach((button) => {
    button.addEventListener("click", () => {
      const dialog = document.getElementById("account-dialog");
      const form = dialog.querySelector("form");
      form.elements.id.value = button.dataset.id;
      form.elements.category_id.value = button.dataset.categoryId || "";
      form.elements.name.value = button.dataset.name || "";
      const urlFields = ["sfdc", "subdomain", "csm_link", "ae_link", "chatapp_channel"];
      const displayNames = { csm_link: button.dataset.csmName || "", ae_link: button.dataset.aeName || "" };
      urlFields.forEach((field) => {
        const div = form.querySelector(`[data-field="${field}"]`);
        const hidden = form.elements[field];
        const value = button.dataset[field] || "";
        if (div) div.innerHTML = renderUrlFieldValue(field, value, displayNames[field] || "");
        if (hidden) hidden.value = value;
      });
      if (form.elements.csm_name) form.elements.csm_name.value = button.dataset.csmName || "";
      if (form.elements.ae_name) form.elements.ae_name.value = button.dataset.aeName || "";
      if (form.elements.arr) form.elements.arr.value = button.dataset.arr || "";
      if (form.elements.churn_risk) form.elements.churn_risk.value = button.dataset.churn_risk || "";
      setFormRichField(form, "details", button.dataset.details);
      const tagIDs = (button.dataset.tagIds || "").split(",").filter(Boolean);
      form.querySelectorAll('input[name="tag_ids"]').forEach((checkbox) => {
        checkbox.checked = tagIDs.includes(checkbox.value);
      });
      dialog.showModal();
    });
  });

  // Account details panel (right side): open on account name click.
  function dueLabel(dueDate) {
    if (!dueDate) return "—";
    const t = new Date(dueDate + "T00:00:00");
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    const tomorrow = new Date(today);
    tomorrow.setDate(tomorrow.getDate() + 1);
    const yesterday = new Date(today);
    yesterday.setDate(yesterday.getDate() - 1);
    if (t.getTime() === today.getTime()) return "Today";
    if (t.getTime() === tomorrow.getTime()) return "Tomorrow";
    if (t.getTime() === yesterday.getTime()) return "Yesterday";

    // Dates in the current Monday-Sunday week show the weekday name.
    const weekStart = new Date(today);
    weekStart.setDate(today.getDate() - ((today.getDay() + 6) % 7));
    const weekEnd = new Date(weekStart);
    weekEnd.setDate(weekStart.getDate() + 6);
    if (t >= weekStart && t <= weekEnd) {
      const weekdays = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
      return weekdays[t.getDay()];
    }

    // Everything else is "day month"; include the year whenever the due date
    // is outside the current calendar year.
    const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    let label = `${t.getDate()} ${months[t.getMonth()]}`;
    if (t.getFullYear() !== today.getFullYear()) label += ` ${t.getFullYear()}`;
    return label;
  }

  // Tracks what the details panel is currently showing so the back button can
  // return to the account details when a task is open.
  let panelView = null;

  function openAccountDetails(accountId) {
    const panel = document.getElementById("account-details-panel");
    const content = document.getElementById("panel-account-content");
    const nameEl = document.getElementById("panel-account-name");
    const headerActions = document.getElementById("panel-header-actions");
    panelView = { type: "account", accountId };
    if (!panel) return; // Panel element was removed (e.g., main.innerHTML swap); bail out
    panel.removeAttribute("hidden");
    selectTaskRow(null);
    window.applySplit?.();
    headerActions.innerHTML = "";

    // Re-select the account row since innerHTML swap removed the .selected class
    const accountRow = document.querySelector(`[data-account-id="${accountId}"]`);
    if (accountRow) accountRow.classList.add("selected");

    (async () => {
      try {
        const showDoneCheckbox = document.querySelector("input[name='show_done'][type='checkbox']");
        const showDone = showDoneCheckbox ? showDoneCheckbox.checked : false;
        const [account, tasks] = await Promise.all([
          api("GET", `/api/v1/accounts/${accountId}`),
          api("GET", `/api/v1/tasks?account_id=${encodeURIComponent(accountId)}${showDone ? "&include_done=1" : ""}`),
        ]);

        nameEl.textContent = account.name || "Account";
        nameEl.title = account.name || "Account";

        const tagHtml = account.tags && account.tags.length
          ? account.tags.map((t) => `<span class="tag-chip" style="--tag-color: ${escapeHtml(t.color)}">${escapeHtml(t.name)}</span>`).join("")
          : "—";

        headerActions.innerHTML = `
          <button class="button ghost tiny" data-edit-account-from-panel data-id="${account.id}" title="Edit account">Edit</button>
          <button class="button ghost tiny archive" data-delete="/api/v1/accounts/${account.id}" title="Archive account">📦</button>
        `;
        content.innerHTML = `
          <dl class="details-list">
            <div class="details-row">
              <dt>Category</dt>
              <dd>${escapeHtml(account.category_name || "—")}</dd>
            </div>
            <div class="details-row">
              <dt>SFDC</dt>
              <dd>${account.sfdc ? renderLink(account.sfdc) : "—"}</dd>
            </div>
            <div class="details-row">
              <dt>Subdomain</dt>
              <dd>${account.subdomain ? renderLink(account.subdomain) : "—"}</dd>
            </div>
            <div class="details-row">
              <dt>CSM</dt>
              <dd>${account.csm_link ? `<a href="${escapeHtml(account.csm_link)}" target="_blank" rel="noopener noreferrer">${escapeHtml(account.csm_name || account.csm_link)}</a>` : account.csm_name ? escapeHtml(account.csm_name) : "—"}</dd>
            </div>
            <div class="details-row">
              <dt>AE</dt>
              <dd>${account.ae_link ? `<a href="${escapeHtml(account.ae_link)}" target="_blank" rel="noopener noreferrer">${escapeHtml(account.ae_name || account.ae_link)}</a>` : account.ae_name ? escapeHtml(account.ae_name) : "—"}</dd>
            </div>
            <div class="details-row">
              <dt>ChatApp channel</dt>
              <dd>${account.chatapp_channel ? renderLink(account.chatapp_channel) : "—"}</dd>
            </div>
            <div class="details-row">
              <dt>ARR</dt>
              <dd>${account.arr ? escapeHtml(account.arr) : "—"}</dd>
            </div>
            <div class="details-row">
              <dt>Churn risk</dt>
              <dd>${account.churn_risk === true ? "At risk" : account.churn_risk === false ? "Not at risk" : "—"}</dd>
            </div>
            <div class="details-row">
              <dt>Renewal date</dt>
              <dd>${account.renewal_date ? dueLabel(account.renewal_date) : "—"}</dd>
            </div>
            <div class="details-row">
              <dt>Details</dt>
              <dd>${renderNote(account.details)}</dd>
            </div>
            <div class="details-row">
              <dt>Tags</dt>
              <dd>${tagHtml}</dd>
            </div>
          </dl>
          <section class="panel-tasks">
            <div class="panel-tasks-header">
              <h3>Tasks (${tasks ? tasks.length : 0})</h3>
              <button class="button small" data-add-task-from-panel data-account-id="${account.id}">Add task</button>
            </div>
            ${tasks && tasks.length
            ? `<ul class="task-list" data-task-list data-account-id="${account.id}">${tasks.map((t) => {
              const overdue = t.overdue ? `<span class="badge overdue-badge">⚠ Overdue</span>` : "";
              const done = t.completed ? `<span class="badge done">done</span>` : "";
              const overdueDays = t.overdue_days ? ` ${t.overdue_days}d` : "";
              return `<li class="task-row ${t.completed ? "done" : ""} ${t.overdue ? "overdue" : ""}"
                    data-task-id="${t.id}"
                    data-due-date="${t.due_date}"
                    data-sort-order="${t.sort_order}"
                    data-account-id="${t.account_id}"
                    draggable="true">
                    <span class="drag-handle" title="Drag within the same due date" aria-hidden="true">⋮⋮</span>
                    <input type="checkbox" class="task-check" data-toggle-task="${t.id}" ${t.completed ? "checked" : ""} aria-label="Mark task complete or incomplete">
                    <div class="task-main">
                      <button type="button" class="task-title task-title-link" data-open-task-details data-id="${t.id}" data-account-id="${account.id}" draggable="true">${escapeHtml(t.title)}</button>
                      <span class="count">${t.notes_count || 0} ${t.notes_count === 1 ? "note" : "notes"}</span>
                      <div class="task-meta">
                        <span class="due">${dueLabel(t.due_date)}</span>
                        ${t.overdue ? `<span class="badge overdue-badge">⚠ Overdue${overdueDays}</span>` : ""}
                      </div>
                    </div>
                  </li>`;
            }).join("")}</ul>`
            : '<p class="muted empty-inline">No tasks.</p>'}
          </section>
        `;
        setupTaskDrag(content);
      } catch (error) {
        content.innerHTML = `<p class="muted">Error loading account: ${escapeHtml(error.message)}</p>`;
      }
    })();
  }

  // openTaskDetails replaces the account details in the right split pane with
  // the task's details. origin records where the task was opened from so the
  // back button can return there: the account details on the Accounts page, or
  // the plain Due date list on the Due date page.
  function openTaskDetails(taskId, accountId, origin = "accounts") {
    const panel = document.getElementById("account-details-panel");
    const content = document.getElementById("panel-account-content");
    const nameEl = document.getElementById("panel-account-name");
    const headerActions = document.getElementById("panel-header-actions");
    panelView = { type: "task", taskId, accountId, origin };
    panel.removeAttribute("hidden");
    selectTaskRow(taskId);
    window.applySplit?.();
    headerActions.innerHTML = "";

    (async () => {
      try {
        const [task, notes] = await Promise.all([
          api("GET", `/api/v1/tasks/${taskId}`),
          api("GET", `/api/v1/tasks/${taskId}/notes`),
        ]);

        nameEl.textContent = task.title || "Task";
        nameEl.title = task.title || "Task";

        const notesHtml = Array.isArray(notes) && notes.length
          ? notes.map((n) => renderPanelNote(n)).join("")
          : '<p class="muted empty-inline">No notes yet.</p>';

        const status = task.completed ? "Done" : (task.overdue ? "Overdue" : "Open");
        headerActions.innerHTML = `
          <button class="button ghost tiny" type="button" data-edit-task data-id="${task.id}" data-title="${escapeAttr(task.title)}" data-ticket="${escapeAttr(task.ticket || "")}" data-due-date="${task.due_date}" data-details="${escapeAttr(task.details || "")}" data-completed="${task.completed ? 1 : 0}" title="Edit task">Edit</button>
          <button class="button ghost tiny archive" type="button" data-delete="/api/v1/tasks/${task.id}" title="Archive task">📦</button>
        `;

        // Fetch account details to display account info on the task details panel
        let accountInfoHtml = "";
        try {
          const account = await api("GET", `/api/v1/accounts/${task.account_id}`);
          accountInfoHtml = `
            ${account.sfdc ? `<div class="details-row"><dt>SFDC</dt><dd>${renderNote(account.sfdc)}</dd></div>` : ""}
            ${account.subdomain ? `<div class="details-row"><dt>Subdomain</dt><dd>${renderNote(account.subdomain)}</dd></div>` : ""}
            ${account.csm_link ? `<div class="details-row"><dt>CSM</dt><dd><a href="${escapeHtml(account.csm_link)}" target="_blank" rel="noopener noreferrer">${escapeHtml(account.csm_name || account.csm_link)}</a></dd></div>` : ""}
            ${account.ae_link ? `<div class="details-row"><dt>AE</dt><dd><a href="${escapeHtml(account.ae_link)}" target="_blank" rel="noopener noreferrer">${escapeHtml(account.ae_name || account.ae_link)}</a></dd></div>` : ""}
            ${account.chatapp_channel ? `<div class="details-row"><dt>ChatApp channel</dt><dd>${renderNote(account.chatapp_channel)}</dd></div>` : ""}
            ${account.renewal_date ? `<div class="details-row"><dt>Renewal date</dt><dd>${escapeHtml(dueLabel(account.renewal_date))}</dd></div>` : ""}
          `;
        } catch (e) {
          // Account fetch failed - silently skip account info
        }

        content.innerHTML = `
          <div class="panel-actions">
            <label class="check compact panel-task-check" title="Mark task complete or incomplete">
              <input type="checkbox" class="task-check" data-toggle-task="${task.id}" ${task.completed ? "checked" : ""} aria-label="Mark task complete or incomplete">
              Completed
            </label>
          </div>
          <dl class="details-list">
            <div class="details-row"><dt>Due date</dt><dd>${dueLabel(task.due_date)}</dd></div>
            <div class="details-row"><dt>Status</dt><dd>${status}</dd></div>
            <div class="details-row"><dt>Ticket #</dt><dd>${task.ticket ? renderNote(task.ticket) : "—"}</dd></div>
            <div class="details-row"><dt>Details</dt><dd>${renderNote(task.details)}</dd></div>
            ${accountInfoHtml}
          </dl>
          <section class="panel-tasks">
            <div class="panel-tasks-header">
              <h3 id="panel-notes-count">Notes (${Array.isArray(notes) ? notes.length : 0})</h3>
              <button class="button small" type="button" data-add-note-from-panel>Add note</button>
            </div>
            <div class="task-notes-list" id="panel-notes-list">${notesHtml}</div>
            <div class="panel-task-form" id="panel-note-form" hidden>
              <form data-task-id="${task.id}">
                <label>New note
                  <div class="url-field notes-field" contenteditable="true" data-field="note" data-rich placeholder="Add a note... URLs become links"></div>
                  <input type="hidden" name="note" value="">
                </label>
                <button type="button" class="button ghost small" data-close-panel-note>Cancel</button>
                <button class="button small" type="submit">Add note</button>
              </form>
            </div>
          </section>
        `;
      } catch (error) {
        content.innerHTML = `<p class="muted">Error loading task: ${escapeHtml(error.message)}</p>`;
      }
    })();
  }

  // Clicking anywhere on an account row opens its details. Clicks that land
  // on other interactive controls are left alone, and a click that follows a
  // drag or text selection is ignored so drag-and-drop keeps working.
  let accountPointerStart = null;

  function toggleAccountDetails(row) {
    const accountId = row.dataset.accountId;
    if (!accountId) return;
    const wasSelected = row.classList.contains("selected");
    // While viewing one of this account's tasks, clicking its row returns
    // to the account details instead of closing the panel.
    if (wasSelected && panelView?.type === "task" && String(panelView.accountId) === String(accountId)) {
      openAccountDetails(accountId);
      return;
    }
    document.querySelectorAll(".account-row.selected").forEach((r) => {
      r.classList.remove("selected");
    });
    if (wasSelected) {
      // Clicking the same account again closes the details panel.
      const panel = document.getElementById("account-details-panel");
      if (panel) panel.setAttribute("hidden", "");
      panelView = null;
      return;
    }
    row.classList.add("selected");
    openAccountDetails(accountId);
  }

  // Delegated listeners so they survive innerHTML replacement of <main>.
  document.addEventListener("mousedown", (event) => {
    if (event.button !== 0) return;
    const row = event.target.closest(".account-row");
    if (!row) return;
    accountPointerStart = { x: event.clientX, y: event.clientY };
  });
  document.addEventListener("click", (event) => {
    const row = event.target.closest(".account-row");
    if (!row) return;
    if (event.target.closest("a, input, select, textarea, label")) return;
    const button = event.target.closest("button");
    if (button && !button.matches("[data-open-account-details]")) return;
    // Treat a pointer that moved between mousedown and click as a drag or
    // text selection, not as a click on the row.
    if (accountPointerStart) {
      const dx = Math.abs(event.clientX - accountPointerStart.x);
      const dy = Math.abs(event.clientY - accountPointerStart.y);
      accountPointerStart = null;
      if (dx > 4 || dy > 4) return;
    }
    event.preventDefault();
    toggleAccountDetails(row);
  });

  // Back button on the details panel: use document delegation so the listener
  // survives the panel being replaced by in-place filter swaps (the panel lives
  // inside <main>, whose innerHTML is swapped when filters change).
  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-close-account-details]");
    if (!button) return;
    event.preventDefault();
    event.stopPropagation();
    // When a task is open, back returns to where it was opened from. Tasks
    // opened on the Due date page close the panel; tasks opened from an
    // account return to that account's details.
    if (panelView?.type === "task") {
      if (panelView.origin === "tasks") {
        const panel = document.getElementById("account-details-panel");
        if (panel) panel.setAttribute("hidden", "");
        panelView = null;
        selectTaskRow(null);
        return;
      }
      if (panelView.accountId) {
        openAccountDetails(panelView.accountId);
        return;
      }
    }
    const panel = document.getElementById("account-details-panel");
    if (panel) panel.setAttribute("hidden", "");
    panelView = null;
    selectTaskRow(null);
    document.querySelectorAll(".account-row.selected").forEach((row) => {
      row.classList.remove("selected");
    });
  });

  // Mirrors the account row selection: the task whose details are open gets a
  // .selected highlight in the Due date list (including after a drag reload).
  function selectTaskRow(taskId) {
    document.querySelectorAll(".task-row.selected").forEach((row) => {
      row.classList.remove("selected");
    });
    if (!taskId) return;
    document.querySelector(`.task-row[data-task-id="${taskId}"]`)?.classList.add("selected");
  }

  // Task titles rendered directly on a page (the Due date list) open the same
  // details panel as task titles inside the account details. Clicking the open
  // task's title again closes the panel, matching the account row behavior.
  function toggleTaskDetails(taskId, accountId) {
    const sameTaskOpen = panelView?.type === "task"
      && panelView.origin === "tasks"
      && String(panelView.taskId) === String(taskId);
    if (sameTaskOpen) {
      const panel = document.getElementById("account-details-panel");
      if (panel) panel.setAttribute("hidden", "");
      panelView = null;
      selectTaskRow(null);
      return;
    }
    openTaskDetails(taskId, accountId, "tasks");
  }

  document.querySelectorAll("[data-open-task-details]").forEach((button) => {
    if (button.closest("#account-details-panel")) return;
    button.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      toggleTaskDetails(button.dataset.id, button.dataset.accountId);
    });
  });

  // Clicking anywhere else on a due-date task row opens the same details.
  // Interactive controls and the drag handle keep their own behavior, and a
  // pointer that moved between mousedown and click is treated as a drag or
  // text selection, not as a row click.
  // Delegated listeners so they survive innerHTML replacement of <main>.
  document.addEventListener("mousedown", (event) => {
    if (event.button !== 0) return;
    const row = event.target.closest(".task-row");
    if (!row) return;
    row._pointerStart = { x: event.clientX, y: event.clientY };
  });
  document.addEventListener("click", (event) => {
    const row = event.target.closest(".task-row");
    if (!row) return;
    if (event.target.closest("a, input, select, textarea, label, .drag-handle")) return;
    const button = event.target.closest("button");
    const openButton = row.querySelector("[data-open-task-details]");
    if (!openButton || openButton.closest("#account-details-panel")) return;
    if (button && button !== openButton) return;
    const pointerStart = row._pointerStart;
    if (pointerStart) {
      const dx = Math.abs(event.clientX - pointerStart.x);
      const dy = Math.abs(event.clientY - pointerStart.y);
      row._pointerStart = null;
      if (dx > 4 || dy > 4) return;
    }
    event.preventDefault();
    toggleTaskDetails(openButton.dataset.id, openButton.dataset.accountId);
  });

  // Panel action buttons: open task details, edit account, add task, add note
  // Delegated on document so listeners survive the panel being recreated by
  // in-place filter swaps (the panel lives inside <main>, whose innerHTML is
  // swapped when filters change).
  let panelPointerStart = null;
  document.addEventListener("mousedown", (event) => {
    if (!event.target.closest("#account-details-panel")) return;
    if (event.button !== 0) return;
    panelPointerStart = { x: event.clientX, y: event.clientY };
  });

  document.addEventListener("click", (event) => {
    if (!event.target.closest("#account-details-panel")) return;
    const openTaskBtn = event.target.closest("[data-open-task-details]");
    const editBtn = event.target.closest("[data-edit-account-from-panel]");
    const addTaskBtn = event.target.closest("[data-add-task-from-panel]");
    const closeTaskBtn = event.target.closest("[data-close-panel-task]");
    const addNoteBtn = event.target.closest("[data-add-note-from-panel]");
    const closeNoteBtn = event.target.closest("[data-close-panel-note]");

    if (addNoteBtn) {
      event.preventDefault();
      const noteForm = document.getElementById("panel-note-form");
      if (noteForm) {
        noteForm.hidden = false;
        noteForm.querySelector('[contenteditable="true"]')?.focus();
        noteForm.scrollIntoView({ behavior: "smooth" });
      }
      return;
    }

    if (closeNoteBtn) {
      event.preventDefault();
      const noteForm = document.getElementById("panel-note-form");
      if (noteForm) noteForm.hidden = true;
      return;
    }

    // Clicking anywhere on a panel task row opens the task, matching the Due
    // date rows. Controls and the drag handle keep their own behavior, and a
    // pointer that moved between mousedown and click is a drag, not a click.
    if (!openTaskBtn) {
      const row = event.target.closest(".task-row");
      const rowButton = row?.querySelector("[data-open-task-details]");
      if (rowButton && !event.target.closest("a, input, select, textarea, label, .drag-handle, button")) {
        if (panelPointerStart) {
          const dx = Math.abs(event.clientX - panelPointerStart.x);
          const dy = Math.abs(event.clientY - panelPointerStart.y);
          panelPointerStart = null;
          if (dx > 4 || dy > 4) return;
        }
        event.preventDefault();
        openTaskDetails(rowButton.dataset.id, rowButton.dataset.accountId, "accounts");
        return;
      }
    }

    if (openTaskBtn) {
      event.preventDefault();
      event.stopPropagation();
      openTaskDetails(openTaskBtn.dataset.id, openTaskBtn.dataset.accountId, "accounts");
      return;
    }

    if (closeTaskBtn) {
      event.preventDefault();
      const form = document.getElementById("panel-task-form");
      if (form) form.hidden = true;
      return;
    }

    if (editBtn) {
      event.preventDefault();
      event.stopPropagation();
      const id = editBtn.dataset.id;
      api("GET", `/api/v1/accounts/${id}`).then((account) => {
        const dialog = document.getElementById("account-dialog");
        const form = dialog.querySelector("form");
        form.elements.id.value = account.id;
        form.elements.category_id.value = account.category_id || "";
        form.elements.name.value = account.name || "";
        const urlFields = ["sfdc", "subdomain", "csm_link", "ae_link", "chatapp_channel"];
        const displayNames = { csm_link: account.csm_name || "", ae_link: account.ae_name || "" };
        urlFields.forEach((field) => {
          const div = form.querySelector(`[data-field="${field}"]`);
          const hidden = form.elements[field];
          const value = account[field] || "";
          if (div) div.innerHTML = renderUrlFieldValue(field, value, displayNames[field] || "");
          if (hidden) hidden.value = value;
        });
        if (form.elements.csm_name) form.elements.csm_name.value = account.csm_name || "";
        if (form.elements.ae_name) form.elements.ae_name.value = account.ae_name || "";
        if (form.elements.arr) form.elements.arr.value = account.arr || "";
        if (form.elements.churn_risk) form.elements.churn_risk.value = account.churn_risk === true ? "true" : account.churn_risk === false ? "false" : "";
        if (form.elements.renewal_date) form.elements.renewal_date.value = account.renewal_date || "";
        setFormRichField(form, "details", account.details);
        const tagIDs = account.tags ? account.tags.map((t) => String(t.id)) : [];
        form.querySelectorAll('input[name="tag_ids"]').forEach((checkbox) => {
          checkbox.checked = tagIDs.includes(checkbox.value);
        });
        dialog.showModal();
      }).catch((error) => toast(error.message, true));
    }

    if (addTaskBtn) {
      event.preventDefault();
      const accountId = addTaskBtn.dataset.accountId;
      const panelContent = document.getElementById("panel-account-content");
      let taskForm = panelContent.querySelector("#panel-task-form");
      if (!taskForm) {
        taskForm = document.createElement("div");
        taskForm.id = "panel-task-form";
        taskForm.className = "panel-task-form";
        taskForm.innerHTML = `
          <h3>Add Task</h3>
          <form data-api="/api/v1/tasks" data-method="POST" data-kind="task-create" data-account-id="${accountId}">
            <label>Title <input name="title" placeholder="Task name" required maxlength="300"></label>
            <label>Due date <input type="date" name="due_date" required></label>
            <label>Ticket #
              <div class="url-field" contenteditable="true" data-field="ticket" data-rich aria-label="Ticket" placeholder="Ticket # (optional) — URLs and [text](url) become links"></div>
              <input type="hidden" name="ticket" value="">
            </label>
            <label>Details
              <div class="url-field notes-field" contenteditable="true" data-field="details" data-rich aria-label="Details" placeholder="Details (optional) — URLs and [text](url) become links"></div>
              <input type="hidden" name="details" value="">
            </label>
            <button type="button" class="button ghost" data-close-panel-task>Cancel</button>
            <button class="button small" type="submit">Add task</button>
          </form>
        `;
        panelContent.appendChild(taskForm);
      }
      taskForm.hidden = false;
      taskForm.scrollIntoView({ behavior: "smooth" });
    }
  });

  async function openTaskEditDialog(button) {
    const dialog = document.getElementById("task-dialog");
    const form = dialog.querySelector("form");
    const notesList = document.getElementById("task-notes-list");
    form.elements.id.value = button.dataset.id;
    form.elements.title.value = button.dataset.title || "";
    setFormRichField(form, "ticket", button.dataset.ticket || "");
    form.elements.due_date.value = button.dataset.dueDate || "";
    setFormRichField(form, "details", button.dataset.details);
    form.elements.completed.checked = button.dataset.completed === "1";
    notesList.dataset.taskId = button.dataset.id;
    notesList.innerHTML = "";
    try {
      const notes = await api("GET", `/api/v1/tasks/${button.dataset.id}/notes`);
      if (Array.isArray(notes) && notes.length) {
        notesList.innerHTML = notes.map((n) => renderTaskNote(n)).join("");
      } else {
        notesList.textContent = "No notes yet.";
      }
    } catch (error) {
      notesList.textContent = "Could not load notes.";
    }
    dialog.showModal();
  }

  // Delegated so task rows rendered inside the account details panel work too.
  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-edit-task]");
    if (!button) return;
    event.preventDefault();
    openTaskEditDialog(button);
  });

  function renderTaskNote(n) {
    const created = n.created_at ? new Date(n.created_at.replace(" ", "T") + "Z").toLocaleString() : "";
    return `
      <div class="task-note" data-note-id="${n.id}">
        <div class="task-note-header">
          <span class="task-note-text" data-raw-note="${escapeAttr(n.note)}">${renderNote(n.note)}</span>
          <span class="task-note-time">${escapeHtml(created)}</span>
          <button class="button ghost tiny" data-edit-note title="Edit note">✏️</button>
          <button class="button ghost tiny danger" data-delete-note title="Delete note">🗑️</button>
        </div>
        <div class="task-note-actions"></div>
      </div>`;
  }

  // Notes inside the task details panel are read-only; adding a note uses the
  // panel's own inline form.
  function renderPanelNote(n) {
    const created = n.created_at ? new Date(n.created_at.replace(" ", "T") + "Z").toLocaleString() : "";
    return `
      <div class="task-note" data-note-id="${n.id}">
        <div class="task-note-header">
          <span class="task-note-text" data-raw-note="${escapeAttr(n.note)}">${renderNote(n.note)}</span>
          <span class="task-note-time">${escapeHtml(created)}</span>
        </div>
      </div>`;
  }

  // Handle edit/delete clicks on notes inside the task edit dialog.
  document.getElementById("task-dialog")?.addEventListener("click", (event) => {
    const noteEl = event.target.closest(".task-note");
    if (!noteEl) return;

    const noteId = noteEl.dataset.noteId;
    const notesList = noteEl.closest("#task-notes-list");
    if (!notesList) return;
    const taskId = notesList.dataset.taskId || "";
    const textSpan = noteEl.querySelector(".task-note-text");
    const actionsDiv = noteEl.querySelector(".task-note-actions");
    const editBtn = noteEl.querySelector("[data-edit-note]");
    const deleteBtn = noteEl.querySelector("[data-delete-note]");

    // Enter edit mode.
    if (event.target.closest("[data-edit-note]") && editBtn && textSpan && editBtn.textContent === "✏️") {
      event.preventDefault();
      const currentText = textSpan.dataset.rawNote ?? textSpan.textContent;
      const editor = document.createElement("div");
      editor.className = "url-field notes-field task-note-edit";
      editor.contentEditable = "true";
      editor.setAttribute("data-rich", "");
      editor.innerHTML = currentText.trim() ? renderNote(currentText) : "";
      textSpan.replaceWith(editor);
      actionsDiv.style.display = "flex";
      editBtn.style.display = "none";
      deleteBtn.style.display = "none";
      const saveBtn = document.createElement("button");
      saveBtn.type = "button";
      saveBtn.className = "button ghost tiny";
      saveBtn.textContent = "💾";
      saveBtn.title = "Save note";
      const cancelBtn = document.createElement("button");
      cancelBtn.type = "button";
      cancelBtn.className = "button ghost tiny";
      cancelBtn.textContent = "✕";
      cancelBtn.title = "Cancel edit";
      cancelBtn.addEventListener("click", (ev) => {
        ev.preventDefault();
        editor.replaceWith(textSpan);
        actionsDiv.style.display = "none";
        editBtn.style.display = "";
        deleteBtn.style.display = "";
        saveBtn.remove();
        cancelBtn.remove();
      });
      actionsDiv.appendChild(saveBtn);
      actionsDiv.appendChild(cancelBtn);
      editor.focus();
      return;
    }

    // Save note.
    const saveBtn = event.target.closest("button[title='Save note']");
    if (saveBtn) {
      event.preventDefault();
      const editor = noteEl.querySelector(".task-note-edit");
      const note = serializeRichField(editor).trim();
      if (!note) return;
      saveBtn.disabled = true;
      (async () => {
        try {
          const result = await api("PUT", `/api/v1/tasks/${taskId}/notes/${noteId}`, { note });
          // The original text span was replaced by the editor when edit mode
          // started, so build a fresh one from the saved value.
          const span = document.createElement("span");
          span.className = "task-note-text";
          span.dataset.rawNote = result.note;
          span.innerHTML = renderNote(result.note);
          editor.replaceWith(span);
          actionsDiv.style.display = "none";
          editBtn.style.display = "";
          deleteBtn.style.display = "";
          saveBtn.remove();
          actionsDiv.querySelector("button[title='Cancel edit']").remove();
          toast("Note updated");
        } catch (error) {
          toast(error.message, true);
        } finally {
          saveBtn.disabled = false;
        }
      })();
      return;
    }

    // Delete note.
    if (event.target.closest("[data-delete-note]")) {
      event.preventDefault();
      if (!window.confirm("Delete this note?")) return;
      deleteBtn.disabled = true;
      (async () => {
        try {
          await api("DELETE", `/api/v1/tasks/${taskId}/notes/${noteId}`);
          noteEl.remove();
          if (notesList.children.length === 0) {
            notesList.textContent = "No notes yet.";
          }
          toast("Note deleted");
        } catch (error) {
          toast(error.message, true);
        } finally {
          deleteBtn.disabled = false;
        }
      })();
    }
  });

  // Add a note from the task details panel. Delegated because the form is
  // rendered with the task details.
  document.addEventListener("submit", async (event) => {
    const form = event.target.closest("#panel-note-form form");
    if (!form) return;
    event.preventDefault();
    syncAllUrlFields(form);
    const taskId = form.dataset.taskId;
    const noteInput = form.elements.note;
    const note = noteInput.value.trim();
    if (!note || !taskId) return;
    const submitBtn = form.querySelector('button[type="submit"]');
    if (submitBtn) submitBtn.disabled = true;
    try {
      const result = await api("POST", `/api/v1/tasks/${taskId}/notes`, { note });
      const notesList = document.getElementById("panel-notes-list");
      if (notesList) {
        notesList.querySelector(".empty-inline")?.remove();
        notesList.insertAdjacentHTML("beforeend", renderPanelNote(result));
        const countEl = document.getElementById("panel-notes-count");
        if (countEl) {
          countEl.textContent = `Notes (${notesList.querySelectorAll(".task-note").length})`;
        }
      }
      noteInput.value = "";
      const editor = form.querySelector('[contenteditable="true"][data-field="note"]');
      if (editor) editor.innerHTML = "";
      const wrapper = document.getElementById("panel-note-form");
      if (wrapper) wrapper.hidden = true;
      toast("Note added");
    } catch (error) {
      toast(error.message, true);
    } finally {
      if (submitBtn) submitBtn.disabled = false;
    }
  });

  // Save task edit (buttons are outside the form, at the bottom of the dialog).
  document.getElementById("task-dialog")?.addEventListener("click", (event) => {
    const saveBtn = event.target.closest("[data-save-task]");
    if (!saveBtn) return;
    event.preventDefault();
    const form = document.getElementById("task-edit-form");
    form?.requestSubmit();
  });

  // Import.
  const importForm = document.getElementById("import-form");
  const importFormatSelect = importForm.elements.format;
  const importJsonLabel = document.getElementById("import-json-label");
  const importPasteHint = document.getElementById("import-paste-hint");

  function toggleImportJsonVisibility() {
    const isJson = importFormatSelect.value === "json";
    importJsonLabel.style.display = isJson ? "" : "none";
    importPasteHint.style.display = isJson ? "" : "none";
  }
  importFormatSelect.addEventListener("change", toggleImportJsonVisibility);
  toggleImportJsonVisibility();

  importForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const format = importForm.elements.format.value;
    const conflict = importForm.elements.conflict.value;
    const sections = Array.from(importForm.elements.sections)
      .filter(cb => cb.checked)
      .map(cb => cb.value)
      .join(",");
    if (!sections) {
      toast("Select at least one section", true);
      return;
    }
    try {
      let response;
      let result = null;
      if (format === "json") {
        // Prefer pasted content; fall back to uploaded file.
        const json = importForm.elements.json.value.trim();
        if (json) {
          response = await fetch(`/api/v1/import?conflict=${encodeURIComponent(conflict)}&sections=${encodeURIComponent(sections)}`, {
            method: "POST",
            headers: { "X-CSRF-Token": csrf, "Content-Type": "application/json", "Accept": "application/json" },
            body: json,
          });
        } else {
          const file = importForm.elements.file.files[0];
          if (!file) {
            toast("Choose a JSON file first or paste JSON content", true);
            return;
          }
          const body = new FormData();
          body.append("file", file);
          response = await fetch(`/api/v1/import?conflict=${encodeURIComponent(conflict)}&sections=${encodeURIComponent(sections)}`, {
            method: "POST",
            headers: { "X-CSRF-Token": csrf, "Accept": "application/json" },
            body,
          });
        }
        const text = await response.text();
        try { result = text ? JSON.parse(text) : null; } catch { result = null; }
      } else {
        const file = importForm.elements.file.files[0];
        if (!file) {
          toast("Choose a CSV file first", true);
          return;
        }
        const body = new FormData();
        body.append("file", file);
        response = await fetch(`/api/v1/import?conflict=${encodeURIComponent(conflict)}&sections=${encodeURIComponent(sections)}`, {
          method: "POST",
          headers: { "X-CSRF-Token": csrf, "Accept": "application/json" },
          body,
        });
        const text = await response.text();
        try { result = text ? JSON.parse(text) : null; } catch { result = null; }
      }
      if (!response.ok) throw new Error(result?.error || `Import failed (${response.status})`);
      toast(`Imported ${result.categories} categories, ${result.tags} tags, ${result.accounts} accounts, ${result.tasks} tasks`);
      closeDialog(document.getElementById("import-dialog"));
      setTimeout(() => window.location.reload(), 900);
    } catch (error) {
      toast(error.message, true);
    }
  });
  // Export.
  const exportForm = document.getElementById("export-form");
  exportForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const format = exportForm.elements.format.value;
    const activeOnly = exportForm.elements.active_only.value;
    const sections = Array.from(exportForm.elements.sections)
      .filter(cb => cb.checked)
      .map(cb => cb.value)
      .join(",");
    if (!sections) {
      toast("Select at least one section", true);
      return;
    }
    closeDialog(document.getElementById("export-dialog"));
    const url = `/api/v1/export?format=${encodeURIComponent(format)}&active_only=${encodeURIComponent(activeOnly)}&sections=${encodeURIComponent(sections)}`;
    const a = document.createElement("a");
    a.href = url;
    a.download = `mirador-${new Date().toISOString().slice(0, 10)}.${format === "json" ? "json" : "csv"}`;
    a.click();
  });

  // Drag-and-drop: same due date only. Cross-date drops are blocked with a
  // visible marker and an explanation; rescheduling uses the edit dialog.
  let dragState = null;

  function clearDragClasses() {
    document.querySelectorAll(".drag-over, .drag-blocked").forEach((element) => {
      element.classList.remove("drag-over", "drag-blocked");
    });
  }

  function canDropOn(element) {
    if (!dragState) return false;
    const container = element.closest("[data-task-list]");
    // The flat due-date list is date-scoped, so rows from different accounts
    // in the same date section can be reordered. Only an account-scoped list
    // (data-account-id on the list) restricts drops to that account.
    const dueDate = element.dataset.dueDate || element.dataset.date || container?.dataset.date;
    if (dueDate && dueDate !== dragState.dueDate) {
      return false;
    }
    const accountID = container?.dataset.accountId;
    if (accountID && dragState.accountId && accountID !== dragState.accountId) {
      return false;
    }
    return true;
  }

  // The account details panel lists one account's tasks across several dates.
  // Cross-date drops there are not a supported move, so block them silently;
  // the red target highlight already gives the feedback. On the Due date page
  // keep the hint, where rescheduling by drag is a plausible mistake.
  function notifyCrossDateBlocked(element) {
    if (element.closest("#account-details-panel")) return;
    toast("Cross-date drag is blocked. Open the task and edit its due date to reschedule.", true);
  }

  // Binds drag-and-drop for task lists. Called once for the server-rendered
  // lists and again whenever the account details panel renders its own tasks.
  function setupTaskDrag(root) {
    root.querySelectorAll('.task-row[draggable="true"]').forEach((row) => {
      row.addEventListener("dragstart", (event) => {
        dragState = {
          id: row.dataset.taskId,
          dueDate: row.dataset.dueDate,
          accountId: row.dataset.accountId,
          element: row,
        };
        row.classList.add("dragging");
        event.dataTransfer.effectAllowed = "move";
        event.dataTransfer.setData("text/plain", row.dataset.taskId);
      });
      row.addEventListener("dragend", () => {
        row.classList.remove("dragging");
        clearDragClasses();
        dragState = null;
      });
      row.addEventListener("dragover", (event) => {
        if (!dragState) return;
        event.preventDefault();
        clearDragClasses();
        if (canDropOn(row)) {
          row.classList.add("drag-over");
          event.dataTransfer.dropEffect = "move";
        } else {
          row.classList.add("drag-blocked");
          event.dataTransfer.dropEffect = "none";
        }
      });
      row.addEventListener("dragleave", () => {
        row.classList.remove("drag-over", "drag-blocked");
      });
      row.addEventListener("drop", async (event) => {
        if (!dragState) return;
        event.preventDefault();
        event.stopPropagation();
        const target = row;
        if (!canDropOn(target)) {
          notifyCrossDateBlocked(target);
          clearDragClasses();
          return;
        }
        await dropIntoList(target.closest("[data-task-list]"), event.clientY, target);
      });
    });

    root.querySelectorAll("[data-task-list]").forEach((list) => {
      list.addEventListener("dragover", (event) => {
        if (!dragState) return;
        event.preventDefault();
        clearDragClasses();
        if (canDropOn(list)) {
          list.classList.add("drag-over");
          event.dataTransfer.dropEffect = "move";
        } else {
          list.classList.add("drag-blocked");
          event.dataTransfer.dropEffect = "none";
        }
      });
      list.addEventListener("dragleave", () => {
        list.classList.remove("drag-over", "drag-blocked");
      });
      list.addEventListener("drop", async (event) => {
        if (!dragState) return;
        event.preventDefault();
        event.stopPropagation();
        if (!canDropOn(list)) {
          notifyCrossDateBlocked(list);
          clearDragClasses();
          return;
        }
        await dropIntoList(list, event.clientY, null);
      });
    });

    root.querySelectorAll(".date-group").forEach((group) => {
      group.addEventListener("dragover", (event) => {
        if (!dragState) return;
        event.preventDefault();
        clearDragClasses();
        if (group.dataset.date === dragState.dueDate) {
          group.classList.add("drag-over");
          event.dataTransfer.dropEffect = "move";
        } else {
          group.classList.add("drag-blocked");
          event.dataTransfer.dropEffect = "none";
        }
      });
      group.addEventListener("dragleave", () => {
        group.classList.remove("drag-over", "drag-blocked");
      });
      group.addEventListener("drop", async (event) => {
        if (!dragState) return;
        event.preventDefault();
        if (group.dataset.date !== dragState.dueDate) {
          notifyCrossDateBlocked(group);
          clearDragClasses();
          return;
        }
        const list = group.querySelector("[data-task-list]");
        if (list) await dropIntoList(list, event.clientY, null);
      });
    });
  }

  setupTaskDrag(document);

  async function dropIntoList(list, clientY, targetRow) {
    if (!list || !dragState) return;
    // Snapshot the drag state: dragend fires while the reorder request is in
    // flight and clears the global, which would break the in-place move below.
    const drag = dragState;
    if (targetRow === drag.element) return;
    const sameDateRows = Array.from(list.querySelectorAll('.task-row[draggable="true"]')).filter((row) => {
      if (row === drag.element) return false;
      if (row.dataset.dueDate !== drag.dueDate) return false;
      if (list.dataset.accountId && row.dataset.accountId !== drag.accountId) return false;
      return true;
    });

    let insertAt = sameDateRows.length;
    if (targetRow) {
      const targetIndex = sameDateRows.indexOf(targetRow);
      const rect = targetRow.getBoundingClientRect();
      insertAt = targetIndex >= 0
        ? targetIndex + (clientY > rect.top + rect.height / 2 ? 1 : 0)
        : sameDateRows.length;

    } else {
      for (let i = 0; i < sameDateRows.length; i += 1) {
        const rect = sameDateRows[i].getBoundingClientRect();
        if (clientY < rect.top + rect.height / 2) {
          insertAt = i;
          break;
        }
      }
    }

    const previous = insertAt > 0 ? sameDateRows[insertAt - 1] : null;
    const next = insertAt < sameDateRows.length ? sameDateRows[insertAt] : null;
    const previousOrder = previous ? Number.parseFloat(previous.dataset.sortOrder) : null;
    const nextOrder = next ? Number.parseFloat(next.dataset.sortOrder) : null;

    let order;
    if (previousOrder !== null && nextOrder !== null) {
      order = (previousOrder + nextOrder) / 2;
    } else if (previousOrder !== null) {
      order = previousOrder + 1;
    } else if (nextOrder !== null) {
      order = nextOrder - 1;
    } else {
      order = 0;
    }

    try {
      await api("PATCH", `/api/v1/tasks/${drag.id}/reorder`, {
        due_date: drag.dueDate,
        sort_order: order,
      });
      clearDragClasses();
      // Reorder in place: no reload, so the panel, selection and scroll stay
      // exactly where they are. The new sort_order keeps later drags in this
      // page consistent with the order the server just stored.
      const row = drag.element;
      if (next && next.parentNode) {
        next.parentNode.insertBefore(row, next);
      } else if (previous && previous.parentNode) {
        previous.parentNode.insertBefore(row, previous.nextSibling);
      } else {
        list.appendChild(row);
      }
      row.dataset.sortOrder = String(order);
    } catch (error) {
      toast(error.message, true);
    }
  }

  // Drag-and-drop: reorder categories.
  let dragCategoryState = null;

  function setupCategoryDrag(root) {
    root.querySelectorAll('[data-draggable-category]').forEach((category) => {
      category.addEventListener("dragstart", (event) => {
        // Prevent the dragstart from bubbling into nested/ancestor draggable
        // elements (e.g. accounts inside a category) so that dragging a category
        // only sets dragCategoryState and never corrupts dragAccountState.
        event.stopPropagation();
        dragCategoryState = {
          id: category.dataset.categoryId,
          sortOrder: Number.parseFloat(category.dataset.categorySortOrder) || 0,
          element: category,
        };
        category.classList.add("dragging");
        event.dataTransfer.effectAllowed = "move";
        event.dataTransfer.setData("text/plain", category.dataset.categoryId);
      });
      category.addEventListener("dragend", () => {
        category.classList.remove("dragging");
        clearDragClasses();
        dragCategoryState = null;
      });
      category.addEventListener("dragover", (event) => {
        if (!dragCategoryState && !dragAccountState) return;
        event.preventDefault();
        // When an account is being dragged over an account row inside this
        // category, let that row keep the hover highlight; only mark the
        // category itself when hovering its header or empty space.
        if (dragAccountState && event.target.closest('[data-draggable-account]')) {
          return;
        }
        clearDragClasses();
        category.classList.add("drag-over");
        event.dataTransfer.dropEffect = "move";
      });
      category.addEventListener("dragleave", () => {
        category.classList.remove("drag-over");
      });
      category.addEventListener("drop", async (event) => {
        if (!dragCategoryState && !dragAccountState) return;
        event.preventDefault();
        event.stopPropagation();
        const target = category;
        if (dragAccountState) {
          // Account dropped onto this category (e.g. an empty category has no
          // account row to receive the drop, so the event bubbles here).
          await dropAccountIntoPosition(target, event.clientY, dragAccountState.element);
          return;
        }
        if (target === dragCategoryState.element) {
          clearDragClasses();
          return;
        }
        await dropCategoryIntoPosition(target, event.clientY, dragCategoryState.element);
      });
    });
  }

  setupCategoryDrag(document);

  async function dropCategoryIntoPosition(target, clientY, draggedElement) {
    if (!dragCategoryState) return;
    const categories = Array.from(document.querySelectorAll('[data-draggable-category]')).filter((c) => c !== draggedElement);
    let insertAt = categories.length;
    const rect = target.getBoundingClientRect();
    const targetIndex = categories.indexOf(target);
    if (targetIndex >= 0) {
      insertAt = targetIndex + (clientY > rect.top + rect.height / 2 ? 1 : 0);
    } else {
      for (let i = 0; i < categories.length; i += 1) {
        const r = categories[i].getBoundingClientRect();
        if (clientY < r.top + r.height / 2) {
          insertAt = i;
          break;
        }
      }
    }

    const previous = insertAt > 0 ? categories[insertAt - 1] : null;
    const next = insertAt < categories.length ? categories[insertAt] : null;
    const previousOrder = previous ? Number.parseFloat(previous.dataset.categorySortOrder) || 0 : null;
    const nextOrder = next ? Number.parseFloat(next.dataset.categorySortOrder) || 0 : null;

    let order;
    if (previousOrder !== null && nextOrder !== null) {
      order = (previousOrder + nextOrder) / 2;
    } else if (previousOrder !== null) {
      order = previousOrder + 1;
    } else if (nextOrder !== null) {
      order = nextOrder - 1;
    } else {
      order = 0;
    }

    try {
      await api("PATCH", `/api/v1/categories/${dragCategoryState.id}/reorder`, {
        sort_order: order,
      });
      clearDragClasses();
      reloadPreservingScroll();
    } catch (error) {
      toast(error.message, true);
    }
  }

  // Drag-and-drop: move accounts between categories.
  let dragAccountState = null;

  document.querySelectorAll('[data-draggable-account]').forEach((account) => {
    account.addEventListener("dragstart", (event) => {
      // Prevent the dragstart from bubbling up to the ancestor category
      // (which is also draggable) so that dragging an account only sets
      // dragAccountState and never populates dragCategoryState. Without this,
      // dragging an account would mark the whole category as the drag source
      // and dropping could reorder the category instead of moving the account.
      event.stopPropagation();
      dragAccountState = {
        id: account.dataset.accountId,
        sortOrder: Number.parseFloat(account.dataset.accountSortOrder) || 0,
        element: account,
      };
      account.classList.add("dragging");
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData("text/plain", account.dataset.accountId);
    });
    account.addEventListener("dragend", () => {
      account.classList.remove("dragging");
      clearDragClasses();
      dragAccountState = null;
    });
    account.addEventListener("dragover", (event) => {
      if (!dragAccountState) return;
      event.preventDefault();
      clearDragClasses();
      account.classList.add("drag-over");
      event.dataTransfer.dropEffect = "move";
    });
    account.addEventListener("dragleave", () => {
      account.classList.remove("drag-over");
    });
    account.addEventListener("drop", async (event) => {
      if (!dragAccountState) return;
      event.preventDefault();
      event.stopPropagation();
      const target = account;
      if (target === dragAccountState.element) {
        clearDragClasses();
        return;
      }
      await dropAccountIntoPosition(target, event.clientY, dragAccountState.element);
    });
  });

  // Keep a category header and empty-state message in sync after an account
  // moves without a reload.
  function syncAccountCategory(category) {
    const list = category.querySelector(".account-list");
    if (!list) return;
    const count = list.querySelectorAll("[data-draggable-account]").length;
    const countEl = category.querySelector(".category-head .count");
    if (countEl) countEl.textContent = `${count} ${count === 1 ? "account" : "accounts"}`;
    const empty = category.querySelector(".empty-inline");
    if (count === 0 && !empty) {
      const message = document.createElement("p");
      message.className = "muted empty-inline";
      message.textContent = "No accounts in this category.";
      list.after(message);
    } else if (count > 0 && empty) {
      empty.remove();
    }
  }

  async function dropAccountIntoPosition(target, clientY, draggedElement) {
    if (!dragAccountState) return;
    // dragend clears the global while the move request is in flight.
    const drag = dragAccountState;
    const targetCategoryElement = target.closest('[data-category-id]');
    if (!targetCategoryElement) {
      toast("Cannot move account outside a category.", true);
      clearDragClasses();
      return;
    }
    const targetCategoryId = targetCategoryElement.dataset.categoryId;

    const accounts = Array.from(targetCategoryElement.querySelectorAll('[data-draggable-account]')).filter((a) => a !== draggedElement);
    let insertAt = accounts.length;
    const rect = target.getBoundingClientRect();
    const targetIndex = accounts.indexOf(target);
    if (targetIndex >= 0) {
      insertAt = targetIndex + (clientY > rect.top + rect.height / 2 ? 1 : 0);
    } else {
      for (let i = 0; i < accounts.length; i += 1) {
        const r = accounts[i].getBoundingClientRect();
        if (clientY < r.top + r.height / 2) {
          insertAt = i;
          break;
        }
      }
    }

    const previous = insertAt > 0 ? accounts[insertAt - 1] : null;
    const next = insertAt < accounts.length ? accounts[insertAt] : null;
    const previousOrder = previous ? Number.parseFloat(previous.dataset.accountSortOrder) || 0 : null;
    const nextOrder = next ? Number.parseFloat(next.dataset.accountSortOrder) || 0 : null;

    let order;
    if (previousOrder !== null && nextOrder !== null) {
      order = (previousOrder + nextOrder) / 2;
    } else if (previousOrder !== null) {
      order = previousOrder + 1;
    } else if (nextOrder !== null) {
      order = nextOrder - 1;
    } else {
      order = 0;
    }

    try {
      await api("PATCH", `/api/v1/accounts/${drag.id}/move`, {
        category_id: Number(targetCategoryId),
        sort_order: order,
      });
      clearDragClasses();
      // Reorder/move in place: no reload, so the open panel, selection and
      // scroll stay untouched. A collapsed target has no list to move into,
      // so fall back to the restoring reload.
      const targetList = targetCategoryElement.querySelector(".account-list");
      if (!targetList) {
        reloadPreservingScroll();
        return;
      }
      const sourceCategory = draggedElement.closest("[data-category-id]");
      if (next && next.parentNode) {
        next.parentNode.insertBefore(draggedElement, next);
      } else if (previous && previous.parentNode) {
        previous.parentNode.insertBefore(draggedElement, previous.nextSibling);
      } else {
        targetList.appendChild(draggedElement);
      }
      draggedElement.dataset.accountSortOrder = String(order);
      syncAccountCategory(targetCategoryElement);
      if (sourceCategory && sourceCategory !== targetCategoryElement) {
        syncAccountCategory(sourceCategory);
      }
      // If the account details panel is open for this account, refresh it so
      // the category and other fields reflect the move.
      if (panelView?.type === "account" && String(panelView.accountId) === String(drag.id)) {
        openAccountDetails(drag.id);
      }
    } catch (error) {
      toast(error.message, true);
    }
  }

  // Restore the details panel after a save that required a full reload (for
  // example, editing an account can move its row to another category).
  let restoredPanelView = null;
  try {
    restoredPanelView = JSON.parse(sessionStorage.getItem("mirador.panelView") || "null");
    sessionStorage.removeItem("mirador.panelView");
  } catch (error) {
    restoredPanelView = null;
  }
  if (restoredPanelView) {
    document
      .querySelector(`.account-row[data-account-id="${restoredPanelView.accountId}"]`)
      ?.classList.add("selected");
    if (restoredPanelView.type === "task" && restoredPanelView.taskId) {
      openTaskDetails(restoredPanelView.taskId, restoredPanelView.accountId, restoredPanelView.origin);
    } else if (restoredPanelView.type === "account" && restoredPanelView.accountId) {
      openAccountDetails(restoredPanelView.accountId);
    }
  }

  // Split pane divider: accounts list <-> account details panel.
  // Opens at 50% (or the last dragged position, persisted in localStorage)
  // and can be dragged to adjust the split. Re-initializable after filter
  // swaps replace <main> innerHTML (which detaches the old split elements).
  let splitRightPct = 50;
  let splitEls = null;

  function getSplitEls() {
    const split = document.querySelector(".container.split");
    const divider = document.querySelector(".split-divider");
    const left = document.querySelector(".split-left");
    const right = document.getElementById("account-details-panel");
    return split && divider && left && right ? { split, divider, left, right } : null;
  }

  function setupSplitter() {
    const els = getSplitEls();
    if (!els) return;
    splitEls = els;
    const { split, divider, left, right } = els;

    try {
      const saved = localStorage.getItem("mirador.splitRightPct");
      if (saved) splitRightPct = Number(saved);
    } catch { }
    if (!Number.isFinite(splitRightPct) || splitRightPct < 20 || splitRightPct > 80) splitRightPct = 50;

    function place(value) {
      value = Math.min(Math.max(value, 20), 80);
      splitRightPct = value;
      right.style.flexBasis = value + "%";
      left.style.flexBasis = (100 - value) + "%";
      divider.style.left = (100 - value) + "%";
      divider.setAttribute("aria-valuenow", String(value));
      try { localStorage.setItem("mirador.splitRightPct", String(value)); } catch { }
    }

    let dragging = false;
    let startX = 0;
    let startPct = 50;

    function onMouseMove(event) {
      if (!dragging) return;
      const rect = split.getBoundingClientRect();
      if (!rect.width) return;
      place(startPct - ((event.clientX - startX) / rect.width) * 100);
    }

    function onMouseUp() {
      if (!dragging) return;
      dragging = false;
      divider.classList.remove("dragging");
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
      document.removeEventListener("mousemove", onMouseMove);
    }

    divider.addEventListener("mousedown", (event) => {
      event.preventDefault();
      dragging = true;
      startX = event.clientX;
      startPct = splitRightPct;
      divider.classList.add("dragging");
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp, { once: true });
    });

    // Keyboard: move the divider with arrow keys.
    divider.addEventListener("keydown", (event) => {
      if (event.key === "ArrowLeft") {
        event.preventDefault();
        place(splitRightPct - 1);
      } else if (event.key === "ArrowRight") {
        event.preventDefault();
        place(splitRightPct + 1);
      } else if (event.key === "Home") {
        event.preventDefault();
        place(20);
      } else if (event.key === "End") {
        event.preventDefault();
        place(80);
      }
    });

    // (Re)apply the saved width whenever the details panel is opened.
    window.applySplit = () => place(splitRightPct);
    // A panel restored from sessionStorage is already visible at setup time.
    if (!right.hasAttribute("hidden")) place(splitRightPct);
  }

  setupSplitter();

  // Re-initialize the splitter after filter swaps replace <main> innerHTML,
  // which detaches the old split elements and inserts new ones from the server.
  window.reinitSplitter = setupSplitter;

  // Compact view for the account list. Stored in localStorage and a cookie so
  // the server can render it on the initial page load.
  (function setupCompact() {
    const btn = document.querySelector("[data-toggle-compact]");
    if (!btn) return;
    const apply = (compact) => {
      if (compact) document.body.classList.add("compact");
      else document.body.classList.remove("compact");
      btn.setAttribute("aria-pressed", String(compact));
      try { localStorage.setItem("mirador.compact", String(compact)); } catch { }
      try {
        document.cookie = "mirador_compact=" + String(compact) + "; path=/; max-age=31536000";
      } catch { }
    };
    try {
      const saved = localStorage.getItem("mirador.compact");
      if (saved !== null) apply(saved === "true");
    } catch { }
    btn.addEventListener("click", () => {
      const compact = btn.getAttribute("aria-pressed") !== "true";
      apply(compact);
    });
  })();

  // Update check: compares the running build against the latest GitHub release.
  // Clicking only opens the releases page when an update is actually available.
  (function setupUpdateCheck() {
    const UPDATE_CHECK_REPO = "jplassnibatt/mirador_v2";
    const btn = document.querySelector("[data-check-update]");
    if (!btn) return;
    // Strip a leading "v" so "1.0.0" compares equal to tag "v1.0.0".
    const APP_VERSION = (btn.getAttribute("data-app-version") || "dev").replace(/^v/, "");

    // Simple semver "is a greater than b" for tags like 1.0.0, 1.0.1, 1.1.0.
    function versionGt(a, b) {
      const pa = a.split(".").map(Number);
      const pb = b.split(".").map(Number);
      for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
        const x = pa[i] || 0;
        const y = pb[i] || 0;
        if (x > y) return true;
        if (x < y) return false;
      }
      return false;
    }

    async function checkForUpdates() {
      if (APP_VERSION === "dev") return;
      try {
        const res = await fetch(
          `https://api.github.com/repos/${UPDATE_CHECK_REPO}/releases/latest`,
          { headers: { Accept: "application/vnd.github+json" } },
        );
        if (!res.ok) return;
        const data = await res.json();
        const latest = (data.tag_name || data.name || "").replace(/^v/, "");
        if (!latest) return;

        if (versionGt(latest, APP_VERSION)) {
          btn.classList.add("update-available");
          btn.textContent = `Update available (v${latest})`;
          btn.title = `Update available: v${latest}`;
        } else if (versionGt(APP_VERSION, latest)) {
          btn.classList.remove("update-available");
          btn.textContent = `Ahead of latest version (v${latest})`;
          btn.title = `You are running v${APP_VERSION}, latest is v${latest}`;
        } else {
          btn.classList.remove("update-available");
          btn.textContent = "Up to date";
          btn.title = `Up to date (v${latest})`;
        }
      } catch {
        // Silently fail — network may be unavailable or rate-limited.
      }
    }

    btn.addEventListener("click", (e) => {
      e.preventDefault();
      if (APP_VERSION === "dev") {
        window.open(`https://github.com/${UPDATE_CHECK_REPO}/releases/latest`, "_blank");
        return;
      }
      if (btn.classList.contains("update-available")) {
        window.open(`https://github.com/${UPDATE_CHECK_REPO}/releases/latest`, "_blank");
      }
    });
    checkForUpdates();
  })();
})();
