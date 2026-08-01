(() => {
  const endpointList = document.getElementById("endpointList");
  const endpointGroup = document.getElementById("endpointGroup");
  const endpointMethod = document.getElementById("endpointMethod");
  const endpointPath = document.getElementById("endpointPath");
  const endpointStatus = document.getElementById("endpointStatus");
  const baseUrlInput = document.getElementById("baseUrlInput");
  const requestPreview = document.getElementById("requestPreview");
  const paramForm = document.getElementById("paramForm");
  const paramGrid = document.getElementById("paramGrid");
  const sendButton = document.getElementById("sendButton");
  const resetButton = document.getElementById("resetButton");
  const copyURLButton = document.getElementById("copyURLButton");
  const savedCollectionSelect = document.getElementById("savedCollectionSelect");
  const collectionNameInput = document.getElementById("collectionNameInput");
  const saveCollectionButton = document.getElementById("saveCollectionButton");
  const newCollectionButton = document.getElementById("newCollectionButton");
  const deleteCollectionButton = document.getElementById("deleteCollectionButton");
  const importCollectionButton = document.getElementById("importCollectionButton");
  const exportCollectionButton = document.getElementById("exportCollectionButton");
  const collectionFileInput = document.getElementById("collectionFileInput");
  const collectionMeta = document.getElementById("collectionMeta");
  const responseStatus = document.getElementById("responseStatus");
  const responseMeta = document.getElementById("responseMeta");
  const assertionSummary = document.getElementById("assertionSummary");
  const assertionList = document.getElementById("assertionList");
  const addAssertionButton = document.getElementById("addAssertionButton");
  const tableOutput = document.getElementById("tableOutput");
  const jsonOutput = document.getElementById("jsonOutput");

  const COLLECTION_SCHEMA = "relay.api_console_collection.v1";
  const COLLECTION_STORAGE_KEY = "relay.api_console.collections.v1";
  const MAX_COLLECTIONS = 200;
  const MAX_ASSERTIONS = 20;
  const ASSERTION_TYPES = [
    ["status_equals", "HTTP 状态等于"],
    ["json_path_exists", "JSON 路径存在"],
    ["json_path_equals", "JSON 路径等于"],
    ["json_path_type", "JSON 路径类型"],
    ["duration_lt", "响应耗时小于(ms)"]
  ];

  let endpoints = [];
  let selectedEndpoint = null;
  let activeStream = null;
  let collections = [];
  let activeCollectionId = "";
  let assertions = [];
  let lastResponse = null;

  function renderEndpointList() {
    endpointList.innerHTML = "";
    const groups = [...new Set(endpoints.map((endpoint) => endpoint.group))];
    for (const group of groups) {
      const title = document.createElement("div");
      title.className = "endpoint-group";
      title.textContent = group;
      endpointList.appendChild(title);
      for (const endpoint of endpoints.filter((item) => item.group === group)) {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "endpoint-item" + (endpoint === selectedEndpoint ? " active" : "");
        const method = document.createElement("span");
        method.className = "method " + endpoint.method.toLowerCase();
        method.textContent = endpoint.method;
        const text = document.createElement("span");
        text.className = "endpoint-path";
        text.textContent = endpoint.title;
        const path = document.createElement("span");
        path.className = "endpoint-status";
        path.textContent = endpoint.path;
        button.append(method, text, path);
        button.addEventListener("click", () => selectEndpoint(endpoint));
        endpointList.appendChild(button);
      }
    }
  }

  function renderForm(endpoint) {
    paramGrid.innerHTML = "";
    if (endpoint.fields.length === 0) {
      const empty = document.createElement("div");
      empty.className = "empty-params";
      empty.textContent = "无参数";
      paramGrid.appendChild(empty);
      return;
    }
    for (const field of endpoint.fields) {
      const label = document.createElement("label");
      label.className = "param-field" + (field.type === "textarea" ? " wide" : "");
      const name = document.createElement("span");
      name.textContent = field.label || field.name;
      const tag = document.createElement("em");
      tag.textContent = field.source + (field.required ? " · 必填" : "");
      name.appendChild(tag);
      const input = createFieldInput(field);
      input.name = field.name;
      input.dataset.source = field.source;
      input.dataset.kind = field.type || "text";
      input.dataset.required = field.required ? "true" : "false";
      if (field.target) {
        input.dataset.target = field.target;
      }
      input.value = field.defaultValue || "";
      label.append(name, input);
      paramGrid.appendChild(label);
    }
  }

  function createFieldInput(field) {
    if (field.type === "select") {
      const input = document.createElement("select");
      for (const optionValue of field.options || []) {
        const option = document.createElement("option");
        option.value = optionValue;
        option.textContent = optionValue === "" ? "(empty)" : optionValue;
        input.appendChild(option);
      }
      return input;
    }
    if (field.type === "textarea") {
      const input = document.createElement("textarea");
      input.spellcheck = false;
      return input;
    }
    if (field.type === "checkbox") {
      const input = document.createElement("input");
      input.type = "checkbox";
      input.value = "true";
      input.checked = field.defaultValue === true || field.defaultValue === "true";
      return input;
    }
    const input = document.createElement("input");
    input.type = field.type === "number" || field.type === "integer" ? "number" : "text";
    if (field.type === "number") {
      input.step = "any";
    }
    return input;
  }

  function selectEndpoint(endpoint, options = {}) {
    if (selectedEndpoint !== endpoint) {
      closeActiveStream();
    }
    if (!options.preserveCollection) {
      clearActiveCollection();
    }
    selectedEndpoint = endpoint;
    endpointGroup.textContent = endpoint.group;
    endpointMethod.textContent = endpoint.method;
    endpointMethod.className = "method " + endpoint.method.toLowerCase();
    endpointPath.textContent = endpoint.path;
    endpointStatus.textContent = endpoint.status;
    endpointStatus.className = "api-status " + statusClass(endpoint.status);
    sendButton.disabled = endpoint.status === "planned";
    sendButton.textContent = endpoint.stream ? "连接" : "发送请求";
    renderForm(endpoint);
    renderEndpointList();
    updatePreview();
  }

  function statusClass(status) {
    if (status === "ready") return "ready";
    if (status === "planned") return "planned";
    if (status === "needs-config") return "needs-config";
    return "blocked";
  }

  function fieldEntries() {
    return Array.from(paramGrid.querySelectorAll("[name]"));
  }

  function fieldValue(field) {
    if (field.type === "checkbox") {
      return field.checked ? "true" : "";
    }
    return field.value.trim();
  }

  function typedValue(field) {
    if (field.type === "checkbox") {
      return field.checked;
    }
    const value = fieldValue(field);
    if (value === "") {
      return "";
    }
    if (field.dataset.kind === "integer") {
      return Number.parseInt(value, 10);
    }
    if (field.dataset.kind === "number") {
      return Number.parseFloat(value);
    }
    return value;
  }

  function eastEightTimestamp(date = new Date()) {
    const parts = new Intl.DateTimeFormat("sv-SE", {
      timeZone: "Asia/Shanghai",
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false
    }).format(date).replace(" ", "T");
    return parts + "+08:00";
  }

  function makeID(prefix) {
    if (window.crypto && typeof window.crypto.randomUUID === "function") {
      return prefix + "-" + window.crypto.randomUUID();
    }
    return prefix + "-" + Date.now() + "-" + Math.random().toString(16).slice(2);
  }

  function defaultAssertion() {
    return {
      id: makeID("assertion"),
      type: "status_equals",
      path: "",
      expected: "200"
    };
  }

  function readFormSnapshot() {
    const fields = {};
    for (const field of fieldEntries()) {
      fields[field.name] = field.type === "checkbox" ? field.checked : field.value;
    }
    return fields;
  }

  function applyFormSnapshot(fields) {
    for (const field of fieldEntries()) {
      if (!Object.prototype.hasOwnProperty.call(fields, field.name)) {
        continue;
      }
      if (field.type === "checkbox") {
        field.checked = fields[field.name] === true || fields[field.name] === "true";
      } else {
        field.value = String(fields[field.name] ?? "");
      }
    }
    updatePreview();
  }

  function readCollections() {
    try {
      const raw = window.localStorage.getItem(COLLECTION_STORAGE_KEY);
      if (!raw) {
        return [];
      }
      const parsed = JSON.parse(raw);
      if (!parsed || parsed.schema_version !== COLLECTION_SCHEMA || !Array.isArray(parsed.collections)) {
        return [];
      }
      return parsed.collections.map(normalizeCollection).filter(Boolean).slice(0, MAX_COLLECTIONS);
    } catch (_err) {
      return [];
    }
  }

  function persistCollections() {
    window.localStorage.setItem(COLLECTION_STORAGE_KEY, JSON.stringify({
      schema_version: COLLECTION_SCHEMA,
      updated_at: eastEightTimestamp(),
      collections
    }));
  }

  function normalizeCollection(value) {
    if (!value || typeof value !== "object") {
      return null;
    }
    const endpointID = String(value.endpoint_id || "").trim();
    const name = String(value.name || "").trim().slice(0, 80);
    if (!endpointID || !name) {
      return null;
    }
    const rawFields = value.request && value.request.fields && typeof value.request.fields === "object"
      ? value.request.fields
      : {};
    const fields = {};
    for (const [key, fieldValue] of Object.entries(rawFields)) {
      if (["string", "number", "boolean"].includes(typeof fieldValue) || fieldValue === null) {
        fields[String(key).slice(0, 120)] = fieldValue;
      }
    }
    const normalizedAssertions = Array.isArray(value.assertions)
      ? value.assertions.map(normalizeAssertion).filter(Boolean).slice(0, MAX_ASSERTIONS)
      : [];
    return {
      id: String(value.id || makeID("collection")),
      name,
      endpoint_id: endpointID,
      request: { fields },
      assertions: normalizedAssertions,
      created_at: String(value.created_at || eastEightTimestamp()),
      updated_at: String(value.updated_at || eastEightTimestamp())
    };
  }

  function normalizeAssertion(value) {
    if (!value || typeof value !== "object") {
      return null;
    }
    const type = ASSERTION_TYPES.some(([candidate]) => candidate === value.type)
      ? value.type
      : "status_equals";
    return {
      id: String(value.id || makeID("assertion")),
      type,
      path: String(value.path || "").slice(0, 240),
      expected: String(value.expected ?? "").slice(0, 1000)
    };
  }

  function renderCollectionSelect() {
    savedCollectionSelect.innerHTML = "";
    const placeholder = document.createElement("option");
    placeholder.value = "";
    placeholder.textContent = "已保存集合 (" + collections.length + ")";
    savedCollectionSelect.appendChild(placeholder);
    const ordered = [...collections].sort((left, right) => left.name.localeCompare(right.name, "zh-CN"));
    for (const collection of ordered) {
      const endpoint = endpoints.find((item) => item.id === collection.endpoint_id);
      const option = document.createElement("option");
      option.value = collection.id;
      option.textContent = collection.name + (endpoint ? " · " + endpoint.method + " " + endpoint.path : " · 接口已移除");
      savedCollectionSelect.appendChild(option);
    }
    savedCollectionSelect.value = activeCollectionId;
    deleteCollectionButton.disabled = !activeCollectionId;
  }

  function clearActiveCollection(resetAssertions = true) {
    activeCollectionId = "";
    if (savedCollectionSelect) {
      savedCollectionSelect.value = "";
    }
    if (collectionNameInput) {
      collectionNameInput.value = "";
    }
    if (collectionMeta) {
      collectionMeta.textContent = "尚未选择集合";
    }
    if (deleteCollectionButton) {
      deleteCollectionButton.disabled = true;
    }
    if (resetAssertions) {
      assertions = [defaultAssertion()];
      renderAssertions();
    }
  }

  function loadCollection(collectionID) {
    const collection = collections.find((item) => item.id === collectionID);
    if (!collection) {
      clearActiveCollection();
      return;
    }
    const endpoint = endpoints.find((item) => item.id === collection.endpoint_id);
    if (!endpoint) {
      collectionMeta.textContent = "集合对应接口已不在 catalog 中";
      return;
    }
    activeCollectionId = collection.id;
    selectEndpoint(endpoint, { preserveCollection: true });
    collectionNameInput.value = collection.name;
    applyFormSnapshot(collection.request.fields);
    assertions = collection.assertions.length > 0
      ? collection.assertions.map((item) => ({ ...item }))
      : [defaultAssertion()];
    renderAssertions();
    renderCollectionSelect();
    collectionMeta.textContent = "已加载 · 更新于 " + collection.updated_at;
  }

  function saveCollection() {
    const name = collectionNameInput.value.trim();
    if (!name) {
      collectionMeta.textContent = "请输入集合名称";
      collectionNameInput.focus();
      return;
    }
    if (!selectedEndpoint) {
      collectionMeta.textContent = "请先选择接口";
      return;
    }
    const now = eastEightTimestamp();
    const existing = collections.find((item) => item.id === activeCollectionId);
    const collection = normalizeCollection({
      id: existing ? existing.id : makeID("collection"),
      name,
      endpoint_id: selectedEndpoint.id,
      request: { fields: readFormSnapshot() },
      assertions,
      created_at: existing ? existing.created_at : now,
      updated_at: now
    });
    if (!collection) {
      collectionMeta.textContent = "集合内容无效";
      return;
    }
    if (existing) {
      collections = collections.map((item) => item.id === existing.id ? collection : item);
    } else {
      if (collections.length >= MAX_COLLECTIONS) {
        collectionMeta.textContent = "集合数量已达到 " + MAX_COLLECTIONS + " 个上限";
        return;
      }
      collections.push(collection);
    }
    try {
      persistCollections();
      activeCollectionId = collection.id;
      renderCollectionSelect();
      collectionMeta.textContent = "已保存 · " + now;
    } catch (err) {
      collectionMeta.textContent = "保存失败 · " + err;
    }
  }

  function deleteCollection() {
    if (!activeCollectionId) {
      return;
    }
    collections = collections.filter((item) => item.id !== activeCollectionId);
    try {
      persistCollections();
      clearActiveCollection();
      renderCollectionSelect();
      collectionMeta.textContent = "集合已删除";
    } catch (err) {
      collectionMeta.textContent = "删除失败 · " + err;
    }
  }

  function exportCollections() {
    if (collections.length === 0) {
      collectionMeta.textContent = "没有可导出的集合";
      return;
    }
    const payload = {
      schema_version: COLLECTION_SCHEMA,
      exported_at: eastEightTimestamp(),
      collections
    };
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    const stamp = eastEightTimestamp().replace(/[-:T+]/g, "").slice(0, 14);
    anchor.href = url;
    anchor.download = "relay-api-console-collections-" + stamp + ".json";
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
    collectionMeta.textContent = "已导出 " + collections.length + " 个集合";
  }

  async function importCollections(file) {
    if (!file) {
      return;
    }
    try {
      const payload = JSON.parse(await file.text());
      if (!payload || payload.schema_version !== COLLECTION_SCHEMA || !Array.isArray(payload.collections)) {
        throw new Error("不支持的集合文件格式");
      }
      let imported = 0;
      let skipped = 0;
      const existingIDs = new Set(collections.map((item) => item.id));
      for (const candidate of payload.collections) {
        const normalized = normalizeCollection(candidate);
        if (!normalized || !endpoints.some((item) => item.id === normalized.endpoint_id)) {
          skipped += 1;
          continue;
        }
        if (collections.length >= MAX_COLLECTIONS) {
          skipped += 1;
          continue;
        }
        if (existingIDs.has(normalized.id)) {
          normalized.id = makeID("collection");
        }
        existingIDs.add(normalized.id);
        normalized.name = collections.some((item) => item.name === normalized.name)
          ? normalized.name + " (导入)"
          : normalized.name;
        normalized.created_at = eastEightTimestamp();
        normalized.updated_at = normalized.created_at;
        collections.push(normalized);
        imported += 1;
      }
      persistCollections();
      renderCollectionSelect();
      collectionMeta.textContent = "已导入 " + imported + " 个集合" + (skipped ? "，跳过 " + skipped + " 个" : "");
    } catch (err) {
      collectionMeta.textContent = "导入失败 · " + err.message;
    } finally {
      collectionFileInput.value = "";
    }
  }

  function renderAssertions(results = null) {
    assertionList.innerHTML = "";
    if (assertions.length === 0) {
      const empty = document.createElement("div");
      empty.className = "assertion-empty";
      empty.textContent = "尚未添加断言";
      assertionList.appendChild(empty);
    }
    assertions.forEach((assertion, index) => {
      const row = document.createElement("div");
      row.className = "assertion-row";
      row.dataset.assertionId = assertion.id;

      const result = results ? results[index] : null;
      const marker = document.createElement("span");
      marker.className = "assertion-marker " + (result ? (result.pass ? "pass" : "fail") : "idle");
      marker.textContent = result ? (result.pass ? "通过" : "失败") : "待运行";
      marker.title = result ? result.message : "发送请求后执行";

      const type = document.createElement("select");
      type.setAttribute("aria-label", "断言类型");
      for (const [value, label] of ASSERTION_TYPES) {
        const option = document.createElement("option");
        option.value = value;
        option.textContent = label;
        type.appendChild(option);
      }
      type.value = assertion.type;
      type.addEventListener("change", () => {
        assertion.type = type.value;
        if (assertion.type === "status_equals" && !assertion.expected) {
          assertion.expected = "200";
        }
        renderAssertions();
      });

      const path = document.createElement("input");
      path.setAttribute("aria-label", "JSON 路径");
      path.placeholder = "data.orders[0].status";
      path.value = assertion.path;
      path.disabled = ["status_equals", "duration_lt"].includes(assertion.type);
      path.addEventListener("input", () => {
        assertion.path = path.value;
      });

      const expected = document.createElement("input");
      expected.setAttribute("aria-label", "期望值");
      expected.placeholder = assertion.type === "json_path_type" ? "string / number / array" : "期望值";
      expected.value = assertion.expected;
      expected.disabled = assertion.type === "json_path_exists";
      expected.addEventListener("input", () => {
        assertion.expected = expected.value;
      });

      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "assertion-remove";
      remove.textContent = "×";
      remove.title = "删除断言";
      remove.setAttribute("aria-label", "删除断言");
      remove.addEventListener("click", () => {
        assertions = assertions.filter((item) => item.id !== assertion.id);
        renderAssertions();
      });

      row.append(marker, type, path, expected, remove);
      assertionList.appendChild(row);
    });
    if (!results) {
      assertionSummary.textContent = "未运行";
      assertionSummary.className = "assertion-summary idle";
    }
  }

  function parseExpectedValue(value) {
    const trimmed = String(value).trim();
    if (trimmed === "") {
      return "";
    }
    try {
      return JSON.parse(trimmed);
    } catch (_err) {
      return trimmed;
    }
  }

  function resolveJSONPath(payload, path) {
    let remaining = String(path || "").trim();
    if (remaining === "" || remaining === "$") {
      return { found: true, value: payload };
    }
    if (remaining.startsWith("$")) {
      remaining = remaining.slice(1);
    }
    const tokens = [];
    while (remaining.length > 0) {
      if (remaining.startsWith(".")) {
        remaining = remaining.slice(1);
      }
      const indexMatch = remaining.match(/^\[(\d+)\]/);
      if (indexMatch) {
        tokens.push(Number.parseInt(indexMatch[1], 10));
        remaining = remaining.slice(indexMatch[0].length);
        continue;
      }
      const quotedMatch = remaining.match(/^\[(["'])(.*?)\1\]/);
      if (quotedMatch) {
        tokens.push(quotedMatch[2]);
        remaining = remaining.slice(quotedMatch[0].length);
        continue;
      }
      const propertyMatch = remaining.match(/^[^.\[\]]+/);
      if (propertyMatch) {
        tokens.push(propertyMatch[0]);
        remaining = remaining.slice(propertyMatch[0].length);
        continue;
      }
      return { found: false, value: undefined };
    }
    let value = payload;
    for (const token of tokens) {
      if (value === null || value === undefined || !Object.prototype.hasOwnProperty.call(Object(value), token)) {
        return { found: false, value: undefined };
      }
      value = value[token];
    }
    return { found: true, value };
  }

  function valueType(value) {
    if (value === null) return "null";
    if (Array.isArray(value)) return "array";
    return typeof value;
  }

  function valuesEqual(actual, expected) {
    if (actual && typeof actual === "object") {
      try {
        return JSON.stringify(actual) === JSON.stringify(expected);
      } catch (_err) {
        return false;
      }
    }
    return Object.is(actual, expected);
  }

  function evaluateAssertion(assertion, response) {
    if (!response) {
      return { pass: false, message: "尚无响应" };
    }
    if (assertion.type === "status_equals") {
      const expected = Number.parseInt(assertion.expected, 10);
      const pass = Number.isFinite(expected) && response.status === expected;
      return { pass, message: "HTTP " + response.status + "，期望 " + assertion.expected };
    }
    if (assertion.type === "duration_lt") {
      const expected = Number.parseFloat(assertion.expected);
      const pass = Number.isFinite(expected) && response.elapsed < expected;
      return { pass, message: response.elapsed + "ms，期望小于 " + assertion.expected + "ms" };
    }
    if (!response.hasJSON) {
      return { pass: false, message: "响应不是 JSON" };
    }
    const resolved = resolveJSONPath(response.parsed, assertion.path);
    if (assertion.type === "json_path_exists") {
      return { pass: resolved.found, message: resolved.found ? "路径存在" : "路径不存在" };
    }
    if (!resolved.found) {
      return { pass: false, message: "路径不存在" };
    }
    if (assertion.type === "json_path_type") {
      const actualType = valueType(resolved.value);
      const expectedType = assertion.expected.trim().toLowerCase();
      return { pass: actualType === expectedType, message: "类型 " + actualType + "，期望 " + expectedType };
    }
    const expected = parseExpectedValue(assertion.expected);
    return {
      pass: valuesEqual(resolved.value, expected),
      message: "实际 " + formatValue(resolved.value) + "，期望 " + formatValue(expected)
    };
  }

  function runAssertions(response) {
    lastResponse = response;
    if (assertions.length === 0) {
      renderAssertions([]);
      assertionSummary.textContent = "无断言";
      assertionSummary.className = "assertion-summary idle";
      return;
    }
    const results = assertions.map((assertion) => evaluateAssertion(assertion, response));
    const passed = results.filter((result) => result.pass).length;
    renderAssertions(results);
    assertionSummary.textContent = passed + "/" + results.length + " 通过";
    assertionSummary.className = "assertion-summary " + (passed === results.length ? "pass" : "fail");
  }

  function buildRequest() {
    let path = selectedEndpoint.path;
    const query = new URLSearchParams();
    const body = {};
    for (const field of fieldEntries()) {
      const value = fieldValue(field);
      if (field.dataset.required === "true" && value === "") {
        throw new Error(field.name + " is required");
      }
      if (field.dataset.source === "path") {
        path = path.replace("{" + field.name + "}", encodeURIComponent(value));
      } else if (field.dataset.source === "query") {
        if (value !== "") {
          query.set(field.name, value);
        }
      } else if (field.dataset.source === "body") {
        if (value !== "") {
          body[field.name] = typedValue(field);
        }
      } else if (field.dataset.source === "body_json") {
        if (value !== "") {
          body[field.dataset.target || field.name] = JSON.parse(value);
        }
      }
    }
    const queryText = query.toString();
    const relativeURL = queryText ? path + "?" + queryText : path;
    const base = baseUrlInput.value.trim().replace(/\/+$/, "");
    return {
      method: selectedEndpoint.method,
      relativeURL,
      url: base + relativeURL,
      body
    };
  }

  function updatePreview() {
    try {
      const request = buildRequest();
      requestPreview.textContent = request.method + " " + request.relativeURL;
    } catch (_err) {
      requestPreview.textContent = selectedEndpoint.method + " " + selectedEndpoint.path;
    }
  }

  function setStatus(label, className) {
    responseStatus.textContent = label;
    responseStatus.className = "api-status " + className;
  }

  async function sendRequest(event) {
    if (event && event.preventDefault) {
      event.preventDefault();
    }
    let request;
    try {
      request = buildRequest();
    } catch (err) {
      setStatus("参数错误", "blocked");
      responseMeta.textContent = err.message;
      jsonOutput.textContent = "{}";
      tableOutput.innerHTML = "";
      return;
    }
    if (selectedEndpoint.stream) {
      startStreamRequest(request);
      return;
    }
    closeActiveStream();
    const started = performance.now();
    setStatus("请求中", "planned");
    responseMeta.textContent = request.method + " " + request.relativeURL;
    tableOutput.innerHTML = "";
    jsonOutput.textContent = "";
    const init = {
      method: request.method,
      headers: { "X-Request-ID": "relay-console-" + Date.now() }
    };
    if (!["GET", "HEAD"].includes(request.method) && Object.keys(request.body).length > 0) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(request.body);
    }
    try {
      const response = await fetch(request.url, init);
      const text = await response.text();
      const elapsed = Math.round(performance.now() - started);
      let parsed = null;
      let hasJSON = false;
      try {
        parsed = JSON.parse(text);
        hasJSON = true;
        jsonOutput.textContent = JSON.stringify(parsed, null, 2);
        renderTable(parsed);
      } catch (_err) {
        jsonOutput.textContent = text || "(empty response)";
        tableOutput.innerHTML = "";
      }
      setStatus("HTTP " + response.status, response.ok ? "ready" : "blocked");
      responseMeta.textContent = request.method + " " + request.relativeURL + " · " + elapsed + "ms";
      runAssertions({ status: response.status, elapsed, parsed, text, hasJSON });
    } catch (err) {
      setStatus("请求失败", "blocked");
      responseMeta.textContent = request.method + " " + request.relativeURL;
      jsonOutput.textContent = String(err);
      lastResponse = null;
      renderAssertions();
      assertionSummary.textContent = "请求失败";
      assertionSummary.className = "assertion-summary fail";
    }
  }

  function startStreamRequest(request) {
    closeActiveStream();
    if (!window.EventSource) {
      setStatus("不支持", "blocked");
      responseMeta.textContent = "当前浏览器不支持 EventSource";
      jsonOutput.textContent = "{}";
      tableOutput.innerHTML = "";
      return;
    }

    const started = performance.now();
    const rows = [];
    setStatus("连接中", "planned");
    responseMeta.textContent = request.method + " " + request.relativeURL;
    tableOutput.innerHTML = "";
    jsonOutput.textContent = "waiting for events...";
    lastResponse = null;
    renderAssertions();
    assertionSummary.textContent = "SSE 等待事件";

    const source = new EventSource(request.url);
    activeStream = source;
    const append = (type, event) => {
      let data = event.data || "";
      try {
        data = JSON.parse(data);
      } catch (_err) {
      }
      rows.unshift({
        type,
        id: event.lastEventId || "",
        data
      });
      rows.splice(20);
      jsonOutput.textContent = JSON.stringify(rows, null, 2);
      renderStreamTable(rows);
      setStatus("已连接", "ready");
      responseMeta.textContent = request.method + " " + request.relativeURL + " · " + Math.round(performance.now() - started) + "ms";
    };

    source.addEventListener("open", () => {
      setStatus("已连接", "ready");
    });
    for (const type of ["relay.connected", "relay.heartbeat", "order.changed", "fill.changed", "asset.changed", "positions.changed"]) {
      source.addEventListener(type, (event) => append(type, event));
    }
    source.onmessage = (event) => append("message", event);
    source.onerror = () => {
      setStatus("重连中", "planned");
      responseMeta.textContent = request.method + " " + request.relativeURL + " · EventSource reconnecting";
    };
  }

  function closeActiveStream() {
    if (activeStream) {
      activeStream.close();
      activeStream = null;
    }
  }

  function tableRows(payload) {
    const data = payload && Object.prototype.hasOwnProperty.call(payload, "data") ? payload.data : payload;
    if (!data) {
      return [];
    }
    if (Array.isArray(data)) {
      return data;
    }
    for (const key of ["accounts", "positions", "orders", "fills", "contributions", "strategies"]) {
      if (Array.isArray(data[key])) {
        return data[key];
      }
    }
    if (data.contribution && typeof data.contribution === "object") {
      if (Array.isArray(data.contribution.contributions)) {
        return data.contribution.contributions;
      }
      return [data.contribution];
    }
    for (const key of ["asset", "order", "published"]) {
      if (data[key] && typeof data[key] === "object" && !Array.isArray(data[key])) {
        return [data[key]];
      }
    }
    if (typeof data === "object") {
      return [data];
    }
    return [];
  }

  function renderTable(payload) {
    const rows = tableRows(payload);
    if (rows.length === 0) {
      tableOutput.innerHTML = "";
      return;
    }
    const limitedRows = rows.slice(0, 100);
    const columns = [];
    for (const row of limitedRows) {
      if (!row || typeof row !== "object") {
        continue;
      }
      for (const key of Object.keys(row)) {
        if (!columns.includes(key)) {
          columns.push(key);
        }
      }
    }
    if (columns.length === 0) {
      tableOutput.innerHTML = "";
      return;
    }
    const table = document.createElement("table");
    table.className = "result-table";
    const thead = document.createElement("thead");
    const headerRow = document.createElement("tr");
    for (const column of columns) {
      const th = document.createElement("th");
      th.textContent = column;
      headerRow.appendChild(th);
    }
    thead.appendChild(headerRow);
    const tbody = document.createElement("tbody");
    for (const row of limitedRows) {
      const tr = document.createElement("tr");
      for (const column of columns) {
        const td = document.createElement("td");
        td.textContent = formatValue(row ? row[column] : "");
        tr.appendChild(td);
      }
      tbody.appendChild(tr);
    }
    table.append(thead, tbody);
    tableOutput.innerHTML = "";
    tableOutput.appendChild(table);
  }

  function renderStreamTable(rows) {
    if (rows.length === 0) {
      tableOutput.innerHTML = "";
      return;
    }
    const table = document.createElement("table");
    table.className = "result-table";
    const thead = document.createElement("thead");
    const headerRow = document.createElement("tr");
    for (const column of ["event", "id", "account", "time", "stream"]) {
      const th = document.createElement("th");
      th.textContent = column;
      headerRow.appendChild(th);
    }
    thead.appendChild(headerRow);
    const tbody = document.createElement("tbody");
    for (const row of rows) {
      const data = row.data && typeof row.data === "object" ? row.data : {};
      const values = [
        row.type,
        row.id || data.id || "",
        Array.isArray(data.account_ids) ? data.account_ids.join(",") : "",
        data.time || "",
        data.last_stream_id || ""
      ];
      const tr = document.createElement("tr");
      for (const value of values) {
        const td = document.createElement("td");
        td.textContent = String(value || "");
        tr.appendChild(td);
      }
      tbody.appendChild(tr);
    }
    table.append(thead, tbody);
    tableOutput.innerHTML = "";
    tableOutput.appendChild(table);
  }

  function formatValue(value) {
    if (value === null || value === undefined) {
      return "";
    }
    if (typeof value === "object") {
      return JSON.stringify(value);
    }
    return String(value);
  }

  async function loadCatalog() {
    const response = await fetch("/assets/api-console.catalog.json?v=20260729-0002");
    if (!response.ok) {
      throw new Error("load endpoint catalog failed: HTTP " + response.status);
    }
    endpoints = await response.json();
    collections = readCollections();
    selectEndpoint(endpoints[0]);
    renderCollectionSelect();
  }

  paramForm.addEventListener("input", updatePreview);
  paramForm.addEventListener("submit", sendRequest);
  baseUrlInput.addEventListener("input", updatePreview);
  resetButton.addEventListener("click", () => {
    closeActiveStream();
    renderForm(selectedEndpoint);
    updatePreview();
    lastResponse = null;
    renderAssertions();
  });
  copyURLButton.addEventListener("click", async () => {
    try {
      const request = buildRequest();
      await navigator.clipboard.writeText(request.url);
      responseMeta.textContent = "已复制 " + request.url;
    } catch (err) {
      responseMeta.textContent = String(err);
    }
  });
  savedCollectionSelect.addEventListener("change", () => {
    if (savedCollectionSelect.value) {
      loadCollection(savedCollectionSelect.value);
    } else {
      clearActiveCollection();
    }
  });
  saveCollectionButton.addEventListener("click", saveCollection);
  newCollectionButton.addEventListener("click", () => {
    clearActiveCollection();
    collectionNameInput.focus();
  });
  deleteCollectionButton.addEventListener("click", deleteCollection);
  importCollectionButton.addEventListener("click", () => collectionFileInput.click());
  exportCollectionButton.addEventListener("click", exportCollections);
  collectionFileInput.addEventListener("change", () => importCollections(collectionFileInput.files[0]));
  addAssertionButton.addEventListener("click", () => {
    if (assertions.length >= MAX_ASSERTIONS) {
      assertionSummary.textContent = "最多 " + MAX_ASSERTIONS + " 条";
      assertionSummary.className = "assertion-summary fail";
      return;
    }
    assertions.push(defaultAssertion());
    renderAssertions();
  });

  loadCatalog().catch((err) => {
    setStatus("目录错误", "blocked");
    responseMeta.textContent = String(err);
  });
})();
