import sys
import cv2
from insightface.app import FaceAnalysis

app = FaceAnalysis(name="buffalo_l", providers=["CPUExecutionProvider"])
app.prepare(ctx_id=-1, det_size=(1280, 1280))

img = cv2.imread(sys.argv[1])
if img is None:
    sys.exit("cannot read image")
print("size (h, w):", img.shape[:2], "| mean brightness:", round(float(img.mean()), 1))

rotations = {
    0: None,
    90: cv2.ROTATE_90_CLOCKWISE,
    180: cv2.ROTATE_180,
    270: cv2.ROTATE_90_COUNTERCLOCKWISE,
}
for angle, code in rotations.items():
    test = img if code is None else cv2.rotate(img, code)
    faces = app.get(test)
    widths = [int(f.bbox[2] - f.bbox[0]) for f in faces]
    print(f"rotation {angle:3d}: {len(faces)} faces, widths {widths}")