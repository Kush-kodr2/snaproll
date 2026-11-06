import base64
import hmac
import io
import os
import threading
from pathlib import Path
from typing import Optional

import cv2
import numpy as np
from dotenv import load_dotenv
from fastapi import Depends, FastAPI, File, HTTPException, UploadFile
from fastapi.security import APIKeyHeader
from insightface.app import FaceAnalysis
from PIL import Image, ImageOps

ROOT = Path(__file__).resolve().parent.parent
load_dotenv(ROOT / ".env")

API_KEY = os.environ.get("FACE_SERVICE_KEY", "")
DET_SIZE = int(os.environ.get("DET_SIZE", "1280"))
MAX_BYTES = 15 * 1024 * 1024  # reject uploads over 15 MB

app = FastAPI(title="Face service")

engine = FaceAnalysis(name="buffalo_l", providers=["CPUExecutionProvider"])
engine.prepare(ctx_id=-1, det_size=(DET_SIZE, DET_SIZE))
engine_lock = threading.Lock()  # one inference at a time; the model isn't built for parallel calls

api_key_header = APIKeyHeader(name="X-Internal-Key", auto_error=False)


def check_key(key: Optional[str] = Depends(api_key_header)) -> None:
    # Fails closed: if no key is configured, nothing is allowed.
    if not API_KEY or not key or not hmac.compare_digest(key.encode(), API_KEY.encode()):
        raise HTTPException(status_code=401, detail="invalid or missing key")


def load_image(data: bytes) -> np.ndarray:
    try:
        img = Image.open(io.BytesIO(data))
        img = ImageOps.exif_transpose(img).convert("RGB")  # applies the phone's rotation flag
    except Exception:
        raise HTTPException(status_code=400, detail="could not read image")
    return cv2.cvtColor(np.array(img), cv2.COLOR_RGB2BGR)  # insightface expects BGR


ROTATIONS = [
    (0, None),
    (270, cv2.ROTATE_90_COUNTERCLOCKWISE),
    (90, cv2.ROTATE_90_CLOCKWISE),
    (180, cv2.ROTATE_180),
]


GOOD_SCORE = float(os.environ.get("GOOD_SCORE", "0.80"))  # stop trying rotations once a face is this confident
  # a detection this confident is accepted without trying other rotations


def detect(img: np.ndarray):
    """Try the image as-is. If no confident face is found, also try it rotated and
    keep the rotation that gave the most confident face.
    Returns (faces, image_that_was_used, angle)."""
    best_faces, best_img, best_angle, best_score = [], img, 0, 0.0
    with engine_lock:
        for angle, code in ROTATIONS:
            test = img if code is None else cv2.rotate(img, code)
            faces = engine.get(test)
            top = max((float(f.det_score) for f in faces), default=0.0)
            if top > best_score:
                best_faces, best_img, best_angle, best_score = faces, test, angle, top
            if top >= GOOD_SCORE:
                break
    return best_faces, best_img, best_angle


def sharpness(img: np.ndarray, bbox) -> float:
    """Variance of the Laplacian on a 112x112 version of the face crop.
    Higher = sharper. Blurry faces give low numbers."""
    h, w = img.shape[:2]
    x1, y1, x2, y2 = [int(v) for v in bbox]
    crop = img[max(y1, 0):min(y2, h), max(x1, 0):min(x2, w)]
    if crop.size == 0:
        return 0.0
    gray = cv2.cvtColor(cv2.resize(crop, (112, 112)), cv2.COLOR_BGR2GRAY)
    return float(cv2.Laplacian(gray, cv2.CV_64F).var())

def make_crop(img: np.ndarray, bbox, size: int = 160) -> str:
    """Small JPEG thumbnail of a face, with some margin, as base64."""
    h, w = img.shape[:2]
    x1, y1, x2, y2 = [int(v) for v in bbox]
    pad = int(0.25 * max(x2 - x1, y2 - y1))
    crop = img[max(y1 - pad, 0):min(y2 + pad, h), max(x1 - pad, 0):min(x2 + pad, w)]
    if crop.size == 0:
        return ""
    ch, cw = crop.shape[:2]
    scale = size / max(ch, cw)
    crop = cv2.resize(crop, (max(int(cw * scale), 1), max(int(ch * scale), 1)),
                      interpolation=cv2.INTER_AREA if scale < 1 else cv2.INTER_CUBIC)
    ok, buf = cv2.imencode(".jpg", crop, [cv2.IMWRITE_JPEG_QUALITY, 85])
    return base64.b64encode(buf.tobytes()).decode() if ok else ""

# @app.get("/health")
# def health():
#     return {"status": "ok", "det_size": DET_SIZE}

@app.post("/analyze", dependencies=[Depends(check_key)])
def analyze(file: UploadFile = File(...), crops: bool = False):
    data = file.file.read(MAX_BYTES + 1)
    if len(data) > MAX_BYTES:
        raise HTTPException(status_code=413, detail="image too large (max 15 MB)")

    img = load_image(data)
    faces, used, angle = detect(img)
    height, width = used.shape[:2]

    out = []
    for f in faces:
        x1, y1, x2, y2 = [int(v) for v in f.bbox]
        entry = {
            "bbox": [x1, y1, x2, y2],
            "width": x2 - x1,
            "det_score": round(float(f.det_score), 4),
            "sharpness": round(sharpness(used, f.bbox), 1),
            "embedding_b64": base64.b64encode(
                f.normed_embedding.astype("<f4").tobytes()).decode(),
        }
        if crops:
            entry["crop_b64"] = make_crop(used, f.bbox)
        out.append(entry)

    return {
        "image_width": width,
        "image_height": height,
        "rotated_by": angle,
        "faces": out,
    }