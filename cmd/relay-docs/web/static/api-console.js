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
  const responseStatus = document.getElementById("responseStatus");
  const responseMeta = document.getElementById("responseMeta");
  const tableOutput = document.getElementById("tableOutput");
  const jsonOutput = document.getElementById("jsonOutput");

  let endpoints = [];
  let selectedEndpoint = null;

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
    const input = document.createElement("input");
    input.type = field.type === "number" || field.type === "integer" ? "number" : "text";
    if (field.type === "number") {
      input.step = "any";
    }
    return input;
  }

  function selectEndpoint(endpoint) {
    selectedEndpoint = endpoint;
    endpointGroup.textContent = endpoint.group;
    endpointMethod.textContent = endpoint.method;
    endpointMethod.className = "method " + endpoint.method.toLowerCase();
    endpointPath.textContent = endpoint.path;
    endpointStatus.textContent = endpoint.status;
    endpointStatus.className = "api-status " + statusClass(endpoint.status);
    sendButton.disabled = endpoint.status === "planned";
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
    return field.value.trim();
  }

  function typedValue(field) {
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
      try {
        const parsed = JSON.parse(text);
        jsonOutput.textContent = JSON.stringify(parsed, null, 2);
        renderTable(parsed);
      } catch (_err) {
        jsonOutput.textContent = text || "(empty response)";
        tableOutput.innerHTML = "";
      }
      setStatus("HTTP " + response.status, response.ok ? "ready" : "blocked");
      responseMeta.textContent = request.method + " " + request.relativeURL + " · " + elapsed + "ms";
    } catch (err) {
      setStatus("请求失败", "blocked");
      responseMeta.textContent = request.method + " " + request.relativeURL;
      jsonOutput.textContent = String(err);
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
    for (const key of ["accounts", "positions", "orders", "fills"]) {
      if (Array.isArray(data[key])) {
        return data[key];
      }
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
    const response = await fetch("/assets/api-console.catalog.json");
    if (!response.ok) {
      throw new Error("load endpoint catalog failed: HTTP " + response.status);
    }
    endpoints = await response.json();
    selectEndpoint(endpoints[0]);
  }

  paramForm.addEventListener("input", updatePreview);
  paramForm.addEventListener("submit", sendRequest);
  baseUrlInput.addEventListener("input", updatePreview);
  resetButton.addEventListener("click", () => selectEndpoint(selectedEndpoint));
  copyURLButton.addEventListener("click", async () => {
    try {
      const request = buildRequest();
      await navigator.clipboard.writeText(request.url);
      responseMeta.textContent = "已复制 " + request.url;
    } catch (err) {
      responseMeta.textContent = String(err);
    }
  });

  loadCatalog().catch((err) => {
    setStatus("目录错误", "blocked");
    responseMeta.textContent = String(err);
  });
})();
