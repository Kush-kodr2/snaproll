"use strict";

const form = document.getElementById("withdraw-form");
const msg = document.getElementById("message");
const btn = document.getElementById("submit");

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  msg.className = "";
  msg.textContent = "";
  if (!window.confirm("Delete your face data? This cannot be undone, and you will need a new invite code from the admin to enroll again.")) return;

  btn.disabled = true;
  try {
    const res = await fetch("/enroll/withdraw", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        enrollment_no: document.getElementById("enrollment_no").value.trim(),
        withdraw_code: document.getElementById("withdraw_code").value.trim(),
      }),
    });
    const body = await res.json().catch(() => ({}));
    if (res.ok) {
      msg.className = "ok";
      msg.textContent = body.message || "Your face data has been deleted.";
      form.reset();
    } else {
      msg.className = "error";
      msg.textContent = body.error || "Something went wrong. Please try again.";
    }
  } catch (err) {
    msg.className = "error";
    msg.textContent = "Could not reach the server.";
  } finally {
    btn.disabled = false;
  }
});