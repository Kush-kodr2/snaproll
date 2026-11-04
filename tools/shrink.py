import sys
from pathlib import Path
from PIL import Image, ImageOps

src = Path(sys.argv[1])
img = ImageOps.exif_transpose(Image.open(src)).convert("RGB")
w, h = img.size
for pct in (75, 50, 35, 25):
    out = src.with_name(f"{src.stem}_{pct}pct.jpg")
    img.resize((w * pct // 100, h * pct // 100), Image.LANCZOS).save(out, quality=90)
    print("wrote", out)