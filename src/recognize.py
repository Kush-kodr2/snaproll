import sys
from pathlib import Path

import cv2
import numpy as np
from insightface.app import FaceAnalysis

ROOT = Path(__file__).resolve().parent.parent
KNOWN_DIR = ROOT / "data" / "known"
DEFAULT_GROUP = ROOT / "data" / "group.jpg"
OUT_PATH = ROOT / "data" / "out.jpg"
IMG_EXT = {".jpg", ".jpeg", ".png"}
THRESH = 0.40  # cosine similarity cutoff; tune later

app = FaceAnalysis(name="buffalo_l", providers=["CPUExecutionProvider"])
app.prepare(ctx_id=-1, det_size=(1280, 1280))  # large size helps small faces

ROTATIONS = [
    (0, None),
    (270, cv2.ROTATE_90_COUNTERCLOCKWISE),
    (90, cv2.ROTATE_90_CLOCKWISE),
    (180, cv2.ROTATE_180),
]


def detect_faces(img):
    """Try the image as-is; if no face is found, try rotated copies.
    Returns (faces, image_used, angle)."""
    for angle, code in ROTATIONS:
        test = img if code is None else cv2.rotate(img, code)
        faces = app.get(test)
        if faces:
            return faces, test, angle
    return [], img, 0

def embed_single(path: Path):
    img = cv2.imread(str(path))
    if img is None:
        print(f"skip {path.name}: could not read image")
        return None
    faces, _, angle = detect_faces(img)
    if angle:
        print(f"note {path.parent.name}/{path.name}: was sideways, auto-rotated ({angle})")
    if len(faces) != 1:
        print(f"skip {path.parent.name}/{path.name}: found {len(faces)} faces")
        return None
    return faces[0].normed_embedding


# --- enroll ---
known = {}
for person_dir in sorted(p for p in KNOWN_DIR.iterdir() if p.is_dir()):
    embs = []
    for f in sorted(person_dir.iterdir()):
        if f.suffix.lower() in IMG_EXT:
            e = embed_single(f)
            if e is not None:
                embs.append(e)
    if embs:
        mean = np.mean(embs, axis=0)
        known[person_dir.name] = mean / np.linalg.norm(mean)
        print(f"enrolled {person_dir.name}: {len(embs)} usable photos")

if not known:
    sys.exit("No one enrolled. Check data/known/ and the photos in it.")

names = list(known)
matrix = np.stack([known[n] for n in names])

# --- recognize in group photo ---
group_path = Path(sys.argv[1]) if len(sys.argv) > 1 else DEFAULT_GROUP
img = cv2.imread(str(group_path))
if img is None:
    sys.exit(f"Could not read {group_path}")

faces, img, angle = detect_faces(img)
print(f"\nDetected {len(faces)} faces in {group_path.name}\n")

for face in faces:
    sims = matrix @ face.normed_embedding
    i = int(np.argmax(sims))
    score = float(sims[i])
    label = names[i] if score >= THRESH else "unknown"

    x1, y1, x2, y2 = face.bbox.astype(int)
    print(f"{label:12s} best_guess={names[i]:8s} score={score:.2f} face_width={x2 - x1}px")

    color = (0, 200, 0) if label != "unknown" else (0, 0, 255)
    cv2.rectangle(img, (x1, y1), (x2, y2), color, 2)
    cv2.putText(img, f"{label} {score:.2f}", (x1, max(y1 - 8, 15)),
                cv2.FONT_HERSHEY_SIMPLEX, 0.6, color, 2)

cv2.imwrite(str(OUT_PATH), img)
print(f"\nSaved annotated image to {OUT_PATH}")