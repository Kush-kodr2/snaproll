"use strict";

const TOKEN_KEY = "attendance_token"; // sessionStorage: cleared when the tab closes
let token = sessionStorage.getItem(TOKEN_KEY) || "";
let me = null;
let current = null; // the session on screen: { id, info, rows, reviews }
let busy = false; // true while an upload, decision or close is in flight
const cropUrls = []; // object URLs of review thumbnails, revoked on every re-render

const $ = (id) => document.getElementById(id);

// Builds elements without innerHTML, so text from the server can never inject markup.
function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key === "class") node.className = value;
    else if (key.startsWith("on")) node.addEventListener(key.slice(2), value);
    else node[key] = value;
  }
  node.append(...children);
  return node;
}

class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

async function api(path, { method = "GET", json } = {}) {
  const headers = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  let body;
  if (json !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(json);
  }
  let res;
  try {
    res = await fetch(path, { method, headers, body });
  } catch (err) {
    throw new ApiError(0, "Could not reach the server.");
  }
  const data = await res.json().catch(() => ({}));
  if (res.status === 401 && path !== "/auth/login") {
    logout(true);
    throw new ApiError(401, "Your login expired. Please log in again.");
  }
  if (!res.ok) throw new ApiError(res.status, data.error || `Request failed (${res.status})`);
  return data;
}

function showBanner(message) {
  $("banner").textContent = message || "";
}

function show(view) {
  for (const v of ["login", "home", "session"]) $(`view-${v}`).hidden = v !== view;
  $("topbar").hidden = view === "login";
}

function today() {
  const d = new Date();
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

const STATUS_LABEL = { present: "Present", absent: "Absent", needs_review: "Needs review" };

// ---------- login / logout ----------

function logout(expired = false) {
  token = "";
  me = null;
  current = null;
  sessionStorage.removeItem(TOKEN_KEY);
  location.hash = "";
  show("login");
  showBanner(expired ? "Your login expired. Please log in again." : "");
}

$("login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  showBanner("");
  const btn = $("login-submit");
  btn.disabled = true;
  try {
    const data = await api("/auth/login", {
      method: "POST",
      json: { email: $("email").value.trim(), password: $("password").value },
    });
    token = data.access_token;
    sessionStorage.setItem(TOKEN_KEY, token);
    $("password").value = "";
    await start();
  } catch (err) {
    showBanner(err.message);
  } finally {
    btn.disabled = false;
  }
});

$("logout").addEventListener("click", () => logout());

// ---------- routing ----------

async function start() {
  if (!token) return show("login");
  try {
    me = await api("/auth/me");
  } catch (err) {
    if (err.status !== 401) showBanner(err.message);
    return;
  }
  $("who").textContent = `${me.name} (${me.role})`;
  await route();
}

async function route() {
  showBanner("");
  const m = location.hash.match(/^#\/s\/(\d+)$/);
  try {
    if (m) await openSession(Number(m[1]));
    else await openHome();
  } catch (err) {
    if (err.status === 401) return;
    if (m) await openHome().catch(() => {});
    showBanner(err.message);
  }
}

window.addEventListener("hashchange", () => {
  if (token && me) route();
});

// ---------- home: courses and past sessions ----------

async function openHome() {
  show("home");
  $("past-panel").hidden = true;
  const offerings = await api("/offerings");
  const box = $("offerings");
  box.replaceChildren();
  if (offerings.length === 0) {
    box.append(el("p", { class: "muted", textContent: "No courses are assigned to you yet. Ask the admin to create one." }));
  }
  for (const o of offerings) {
    const newBtn = el("button", { type: "button", textContent: "Take attendance today" });
    newBtn.addEventListener("click", async () => {
      newBtn.disabled = true;
      try {
        const s = await api("/sessions", { method: "POST", json: { offering_id: o.id, session_date: today() } });
        location.hash = `#/s/${s.id}`;
      } catch (err) {
        showBanner(err.message);
        newBtn.disabled = false;
      }
    });
    const pastBtn = el("button", { type: "button", class: "secondary", textContent: "Past sessions" });
    pastBtn.addEventListener("click", () => showPast(o).catch((err) => showBanner(err.message)));

    const subtitle = o.batch_name + (me.role === "admin" ? ` · ${o.teacher_name}` : "");
    box.append(el("div", { class: "card" },
      el("div", { class: "who" },
        el("strong", { textContent: `${o.course_code} · ${o.course_name}` }),
        el("span", { class: "muted", textContent: subtitle })),
      el("div", { class: "actions" }, newBtn, pastBtn)));
  }
}

async function showPast(offering) {
  const sessions = await api(`/sessions?offering_id=${offering.id}`);
  $("past-title").textContent = `Past sessions: ${offering.course_code} · ${offering.batch_name}`;
  const list = $("past-list");
  list.replaceChildren();
  if (sessions.length === 0) list.append(el("li", { class: "muted", textContent: "No sessions yet." }));
  for (const s of sessions) {
    list.append(el("li", { class: "row" },
      el("div", { class: "who" },
        el("strong", { textContent: s.session_date }),
        el("span", { class: "muted", textContent: `${s.status} · ${s.student_count} students` })),
      el("div", { class: "actions" }, el("a", { href: `#/s/${s.id}`, textContent: "Open" }))));
  }
  $("past-panel").hidden = false;
}

// ---------- session screen ----------

async function openSession(id) {
  show("session");
  current = { id, info: null, rows: [], reviews: [] };
  $("upload-log").replaceChildren();
  $("search").value = "";
  await refreshSession();
}

async function refreshSession() {
  const id = current.id;
  const [info, rows, reviews] = await Promise.all([
    api(`/sessions/${id}`),
    api(`/sessions/${id}/attendance`),
    api(`/sessions/${id}/review`),
  ]);
  if (!current || current.id !== id) return; // the teacher navigated away meanwhile
  current.info = info;
  current.rows = rows;
  current.reviews = reviews;
  renderSession();
}

function setControls() {
  document.querySelectorAll("#view-session button, #photo-camera, #photo-gallery").forEach((b) => {
    b.disabled = busy;
  });
}

function renderSession() {
  const { info, rows, reviews } = current;
  const closed = info.status === "closed";
  const count = (s) => rows.filter((r) => r.status === s).length;

  $("session-title").textContent = `${info.course_code} · ${info.course_name}`;
  $("session-meta").textContent = `${info.batch_name} · ${info.session_date} · ${closed ? "closed" : "open"}`;
  $("counts").replaceChildren(
    el("span", { class: "chip present", textContent: `Present ${count("present")}` }),
    el("span", { class: "chip needs_review", textContent: `Needs review ${count("needs_review")}` }),
    el("span", { class: "chip absent", textContent: `Absent ${count("absent")}` }),
  );

  $("upload-panel").hidden = closed;
  $("review-panel").hidden = closed || reviews.length === 0;
  $("close-bar").hidden = closed;
  const pending = count("needs_review");
  $("close-note").textContent = pending ? `${pending} student(s) still need review.` : "";

  renderReviews();
  renderList();
  setControls();
}

async function loadCrop(img, url) {
  try {
    const res = await fetch(url, { headers: { Authorization: `Bearer ${token}` } });
    if (!res.ok) throw new Error("no thumbnail");
    const objectUrl = URL.createObjectURL(await res.blob());
    cropUrls.push(objectUrl);
    img.src = objectUrl;
  } catch (err) {
    img.alt = "Thumbnail unavailable";
  }
}

function renderReviews() {
  cropUrls.splice(0).forEach((u) => URL.revokeObjectURL(u));
  const box = $("review-list");
  box.replaceChildren();
  for (const r of current.reviews) {
    const img = el("img", { class: "crop", alt: `Face suggested as ${r.name}` });
    loadCrop(img, r.crop_url);
    box.append(el("div", { class: "review-card" },
      img,
      el("strong", { textContent: r.name }),
      el("span", { class: "muted", textContent: `${r.enrollment_no} · similarity ${r.score.toFixed(2)}` }),
      el("button", { type: "button", textContent: "Yes, present", onclick: () => decide(r.student_id, "present") }),
      el("button", { type: "button", class: "secondary", textContent: "No, absent", onclick: () => decide(r.student_id, "absent") }),
    ));
  }
}

function renderList() {
  const q = $("search").value.trim().toLowerCase();
  const closed = current.info.status === "closed";
  const list = $("class-list");
  list.replaceChildren();
  for (const r of current.rows) {
    if (q && !`${r.name} ${r.enrollment_no}`.toLowerCase().includes(q)) continue;

    const tags = [el("span", { class: `badge ${r.status}`, textContent: STATUS_LABEL[r.status] || r.status })];
    if (r.method === "manual") tags.push(el("span", { class: "badge manual", textContent: "set by hand" }));
    if (!r.enrolled) tags.push(el("span", { class: "badge notenrolled", textContent: "not enrolled" }));

    const actions = closed ? [] : [
      el("button", { type: "button", class: r.status === "present" ? "small active" : "small", textContent: "Present", onclick: () => decide(r.student_id, "present") }),
      el("button", { type: "button", class: r.status === "absent" ? "small active" : "small", textContent: "Absent", onclick: () => decide(r.student_id, "absent") }),
    ];
    list.append(el("li", { class: "row" },
      el("div", { class: "who" },
        el("strong", { textContent: r.name }),
        el("span", { class: "muted", textContent: r.enrollment_no })),
      el("div", { class: "tags" }, ...tags),
      el("div", { class: "actions" }, ...actions)));
  }
}

$("search").addEventListener("input", () => {
  if (current && current.info) renderList();
});

async function decide(studentId, status) {
  if (busy) return;
  busy = true;
  setControls();
  try {
    await api(`/sessions/${current.id}/attendance/${studentId}`, { method: "PUT", json: { status } });
    await refreshSession();
  } catch (err) {
    showBanner(err.message);
  } finally {
    busy = false;
    setControls();
  }
}

// ---------- photo upload (one photo per request, with progress) ----------

// XMLHttpRequest, not fetch, because only XHR reports upload progress.
function sendPhoto(sessionId, file, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `/sessions/${sessionId}/photos`);
    xhr.setRequestHeader("Authorization", `Bearer ${token}`);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(e.loaded / e.total);
    };
    xhr.upload.onload = () => onProgress(1);
    xhr.onload = () => {
      let body = {};
      try {
        body = JSON.parse(xhr.responseText);
      } catch (err) {
        // not JSON: keep the empty object
      }
      if (xhr.status === 200) resolve(body);
      else reject(new ApiError(xhr.status, body.error || `Upload failed (${xhr.status})`));
    };
    xhr.onerror = () => reject(new ApiError(0, "Could not reach the server."));
    const form = new FormData();
    form.append("photos", file, file.name);
    xhr.send(form);
  });
}

async function uploadOne(file) {
  const li = el("li", { textContent: `${file.name}: starting...` });
  $("upload-log").append(li);
  try {
    const result = await sendPhoto(current.id, file, (fraction) => {
      li.textContent = fraction < 1
        ? `${file.name}: uploading ${Math.round(fraction * 100)}%`
        : `${file.name}: analysing faces, this takes about 20 seconds...`;
    });
    const p = result.photos[0];
    li.textContent = `${file.name}: ${p.faces} faces found, ${p.matched} matched to students, ${p.unmatched} not matched.`;
    li.className = "ok";
  } catch (err) {
    if (err.status === 401) {
      logout(true);
      throw err;
    }
    li.textContent = `${file.name}: ${err.message}`;
    li.className = "error";
  }
}

async function addPhotos(event) {
  const files = Array.from(event.target.files);
  event.target.value = ""; // lets the teacher pick the same file again later
  if (files.length === 0 || busy) return;
  busy = true;
  setControls();
  try {
    for (const file of files) {
      await uploadOne(file);
      try {
        await refreshSession();
      } catch (err) {
        showBanner(err.message);
      }
    }
  } catch (err) {
    // a logout during upload: nothing more to do
  } finally {
    busy = false;
    setControls();
  }
}

$("photo-camera").addEventListener("change", addPhotos);
$("photo-gallery").addEventListener("change", addPhotos);
$("camera-btn").addEventListener("click", () => $("photo-camera").click());
$("gallery-btn").addEventListener("click", () => $("photo-gallery").click());

// ---------- closing ----------

$("close-btn").addEventListener("click", async () => {
  if (busy) return;
  const pending = current.rows.filter((r) => r.status === "needs_review").length;
  const message = pending
    ? `${pending} student(s) still need review. Close anyway? They will be marked absent, and a closed session cannot be changed.`
    : "Close this session? A closed session cannot be changed.";
  if (!window.confirm(message)) return;
  busy = true;
  setControls();
  try {
    await api(`/sessions/${current.id}/close${pending ? "?force=true" : ""}`, { method: "POST" });
    await refreshSession();
  } catch (err) {
    showBanner(err.message);
  } finally {
    busy = false;
    setControls();
  }
});

start();