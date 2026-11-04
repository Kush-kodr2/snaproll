"use strict";

const MAX_SIDE = 1600; // longest side after shrinking: small uploads, faces still large enough
const MIN_PHOTOS = 3;

const form = document.getElementById("enroll-form");
const statusEl = document.getElementById("status");
const errorEl = document.getElementById("error");
const problemsEl = document.getElementById("problems");
const submitBtn = document.getElementById("submit");
const successEl = document.getElementById("success");
const fileInputs = Array.from(form.querySelectorAll('input[type="file"]'));

function showPreview(input) {
  const img = input.closest(".slot").querySelector(".preview");
  if (img.dataset.url) {
    URL.revokeObjectURL(img.dataset.url);
    delete img.dataset.url;
  }
  const file = input.files[0];
  if (!file) {
    img.hidden = true;
    img.removeAttribute("src");
    return;
  }
  const url = URL.createObjectURL(file);
  img.dataset.url = url;
  img.src = url;
  img.hidden = false;
}
fileInputs.forEach((input) => input.addEventListener("change", () => showPreview(input)));

function clearMessages() {
  errorEl.textContent = "";
  problemsEl.replaceChildren();
}

function showError(message, problems = []) {
  errorEl.textContent = message;
  problems.forEach((p) => {
    const li = document.createElement("li");
    li.textContent = p; // textContent, never innerHTML, so server text can't inject markup
    problemsEl.appendChild(li);
  });
}

function setBusy(busy, message = "") {
  submitBtn.disabled = busy;
  statusEl.textContent = message;
}

// Shrinks a photo in the browser. Falls back to the original file if the browser can't.
async function shrink(file) {
  try {
    const bmp = await createImageBitmap(file, { imageOrientation: "from-image" });
    const scale = Math.min(1, MAX_SIDE / Math.max(bmp.width, bmp.height));
    const canvas = document.createElement("canvas");
    canvas.width = Math.round(bmp.width * scale);
    canvas.height = Math.round(bmp.height * scale);
    canvas.getContext("2d").drawImage(bmp, 0, 0, canvas.width, canvas.height);
    bmp.close();
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.9));
    return blob || file;
  } catch (err) {
    return file;
  }
}

function showSuccess(withdrawCode, name) {
  form.reset();
  fileInputs.forEach(showPreview);
  form.hidden = true;
  document.getElementById("success-title").textContent = name ? `${name} is enrolled` : "You're enrolled";
  document.getElementById("withdraw-code").textContent = withdrawCode;
  successEl.hidden = false;
  window.scrollTo(0, 0);
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  clearMessages();

  const enrollmentNo = document.getElementById("enrollment_no").value.trim();
  const code = document.getElementById("code").value.trim();
  const files = fileInputs.map((input) => input.files[0]).filter(Boolean);

  if (!enrollmentNo || !code) return showError("Please enter your enrollment number and invite code.");
  if (files.length < MIN_PHOTOS) return showError(`Please add at least ${MIN_PHOTOS} photos.`);
  if (!document.getElementById("consent").checked) return showError("Please tick the consent box to continue.");

  setBusy(true, "Preparing your photos...");
  try {
    const data = new FormData();
    data.append("enrollment_no", enrollmentNo);
    data.append("code", code);
    data.append("consent", "true");
    for (let i = 0; i < files.length; i++) {
      data.append("photos", await shrink(files[i]), `photo${i + 1}.jpg`);
    }

    setBusy(true, "Checking your photos. This can take up to 30 seconds...");
    const res = await fetch("/enroll", { method: "POST", body: data });
    const body = await res.json().catch(() => ({}));

    if (res.status === 201) {
      showSuccess(body.withdraw_code);
    } else {
      showError(body.error || "Something went wrong. Please try again.", body.problems || []);
    }
  } catch (err) {
    showError("Could not reach the server. Check your connection and try again.");
  } finally {
    setBusy(false);
  }
});

const copyBtn = document.getElementById("copy");
if (navigator.clipboard && window.isSecureContext) {
  copyBtn.addEventListener("click", () =>
    navigator.clipboard.writeText(document.getElementById("withdraw-code").textContent));
} else {
  copyBtn.hidden = true; // the clipboard API needs HTTPS, so hide the button on plain http
}