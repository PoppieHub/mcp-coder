#!/usr/bin/env python3
"""Render the verified MCP Coder Delegator launch demo."""

from __future__ import annotations

import math
import subprocess
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / "marketplace" / "agensi" / "assets"
OUTPUT = ASSETS / "mcp-coder-delegator-demo.mp4"
W, H, FPS, DURATION = 1600, 900, 30, 31

REGULAR = "/System/Library/Fonts/Supplemental/Arial.ttf"
BOLD = "/System/Library/Fonts/Supplemental/Arial Bold.ttf"
MONO = "/System/Library/Fonts/Supplemental/Menlo.ttc"

INK, MUTED = "#F5F8FF", "#93A6BF"
BLUE, GREEN, PURPLE, RED = "#58B7FF", "#65E6A5", "#9B8BFF", "#FF7188"
PANEL, STROKE = "#101D30", "#294362"


def face(size, *, bold=False, mono=False):
    return ImageFont.truetype(MONO if mono else BOLD if bold else REGULAR, size)


F16, F18, F21 = face(16), face(18), face(21)
F18M, F20M = face(18, mono=True), face(20, mono=True)
F19B, F22B, F26B = face(19, bold=True), face(22, bold=True), face(26, bold=True)
F30B, F36B, F44B = face(30, bold=True), face(36, bold=True), face(44, bold=True)
F58B, F76B = face(58, bold=True), face(76, bold=True)


def ease(value):
    value = max(0.0, min(1.0, value))
    return 1 - (1 - value) ** 3


def center(draw, y, text, font, color=INK):
    box = draw.textbbox((0, 0), text, font=font)
    draw.text(((W - box[2] + box[0]) / 2, y), text, font=font, fill=color)


def card(draw, box, *, fill=PANEL, outline=STROKE, radius=22, width=2):
    draw.rounded_rectangle(box, radius, fill=fill, outline=outline, width=width)


def pill(draw, x, y, text, color=BLUE, width=None):
    text_box = draw.textbbox((0, 0), text, font=F18)
    width = width or text_box[2] - text_box[0] + 34
    draw.rounded_rectangle((x, y, x + width, y + 38), 19, fill="#132B43", outline=color, width=2)
    draw.text((x + 17, y + 8), text, font=F18, fill=color)


def check(draw, x, y, radius=18):
    draw.ellipse((x - radius, y - radius, x + radius, y + radius), fill="#143B32", outline=GREEN, width=2)
    draw.line((x - 8, y, x - 2, y + 7, x + 10, y - 8), fill=GREEN, width=4, joint="curve")


def arrow(draw, start, end, color):
    draw.line((*start, *end), fill=color, width=4)
    angle = math.atan2(end[1] - start[1], end[0] - start[0])
    for delta in (2.55, -2.55):
        draw.line((*end, end[0] + 14 * math.cos(angle + delta), end[1] + 14 * math.sin(angle + delta)), fill=color, width=4)


def background():
    image = Image.new("RGB", (W, H), "#07101F")
    pixels = image.load()
    for y in range(H):
        for x in range(W):
            a = max(0.0, 1 - math.hypot(x - 270, y - 120) / 680)
            b = max(0.0, 1 - math.hypot(x - 1350, y - 760) / 760)
            pixels[x, y] = (int(7 + 9 * a + 8 * b), int(16 + 17 * a + 5 * b), int(31 + 31 * a + 29 * b))
    draw = ImageDraw.Draw(image)
    for x in range(0, W, 80):
        draw.line((x, 0, x, H), fill="#10233B")
    for y in range(0, H, 80):
        draw.line((0, y, W, y), fill="#10233B")
    return image


BASE = background()
LOGO = Image.open(ASSETS / "mcp-coder-delegator-logo.png").convert("RGBA")


def canvas(section=None):
    image = BASE.convert("RGBA")
    if section:
        draw = ImageDraw.Draw(image)
        image.alpha_composite(LOGO.resize((54, 54), Image.Resampling.LANCZOS), (56, 35))
        draw.text((126, 40), "MCP CODER DELEGATOR", font=F22B, fill=INK)
        draw.text((126, 69), section.upper(), font=F16, fill=MUTED)
        pill(draw, 1284, 42, "REAL VERIFIED RUN", GREEN, 256)
    return image


def footer(draw, caption, step):
    draw.rounded_rectangle((52, 810, 1548, 864), 27, fill="#091525", outline="#203957", width=2)
    draw.text((80, 826), caption, font=F21, fill=INK)
    for index in range(7):
        draw.ellipse((1410 + index * 18, 830, 1420 + index * 18, 840), fill=GREEN if index <= step else "#263B55")


def intro(progress):
    image = canvas()
    draw = ImageDraw.Draw(image)
    size = int(170 + 22 * ease(progress))
    image.alpha_composite(LOGO.resize((size, size), Image.Resampling.LANCZOS), ((W - size) // 2, 105))
    center(draw, 332, "ONE REQUEST.", F76B)
    center(draw, 420, "BOUNDED DELEGATION.", F76B, BLUE)
    center(draw, 525, "Parallel implementation without giving up final control.", F30B, MUTED)
    x = 390
    for label, color, width in (("2 WORKSTREAMS", BLUE, 240), ("0 SCOPE OVERLAP", PURPLE, 264), ("3/3 CHECKS", GREEN, 220)):
        pill(draw, x, 630, label, color, width)
        x += width + 24
    return image


def contract(draw, x, y, title, path, outcome, color):
    card(draw, (x, y, x + 610, y + 205))
    draw.rounded_rectangle((x + 24, y + 24, x + 112, y + 55), 15, fill=color)
    draw.text((x + 40, y + 30), "TASK", font=F16, fill="#07101F")
    draw.text((x + 130, y + 26), title, font=F26B, fill=INK)
    for row, label, value, value_font, value_color in (
        (82, "WRITE SCOPE", path, F18M, color),
        (122, "ACCEPTANCE", outcome, F18, INK),
        (163, "VERIFY", "npm test", F18M, GREEN),
    ):
        draw.text((x + 28, y + row), label, font=F16, fill=MUTED)
        draw.text((x + 176, y + row - 3), value, font=value_font, fill=value_color)


def contracts(_):
    image = canvas("Contract builder")
    draw = ImageDraw.Draw(image)
    card(draw, (120, 130, 1480, 235), fill="#0D1A2C", outline=BLUE)
    draw.text((154, 154), "REQUEST", font=F19B, fill=BLUE)
    draw.text((154, 190), "Ship backend validation and the independent empty state in parallel.", font=F26B, fill=INK)
    contract(draw, 120, 282, "Backend validation", "backend/validate.js", "Reject blank input", BLUE)
    contract(draw, 870, 282, "Empty-state copy", "frontend/empty-state.js", "Return approved copy", PURPLE)
    arrow(draw, (800, 242), (425, 278), BLUE)
    arrow(draw, (800, 242), (1175, 278), PURPLE)
    card(draw, (290, 530, 1310, 750), fill="#0B1728")
    for index, (left, right) in enumerate((("Outcome", "Concrete and testable"), ("Allowed changes", "One file per worker"), ("Known context", "Passed once — no rediscovery"), ("Acceptance", "Tests must pass before completion"))):
        y = 557 + index * 44
        draw.text((330, y), left.upper(), font=F16, fill=MUTED)
        draw.text((610, y - 2), right, font=F21, fill=INK)
        check(draw, 1264, y + 10, 13)
    footer(draw, "Scope first. Execution second.", 1)
    return image


def scopes(_):
    image = canvas("Write-scope isolation")
    draw = ImageDraw.Draw(image)
    center(draw, 132, "NO OVERLAPPING WRITES", F44B)
    center(draw, 190, "Each worker owns exactly one declared path.", F26B, MUTED)
    for x, title, color, files in ((145, "WORKER A", BLUE, ("backend/", "  validate.js", "  validate.test.js")), (865, "WORKER B", PURPLE, ("frontend/", "  empty-state.js", "  empty-state.test.js"))):
        card(draw, (x, 275, x + 590, 696), outline=color)
        draw.text((x + 40, 315), title, font=F22B, fill=color)
        for index, name in enumerate(files):
            draw.text((x + 55, 390 + index * 58), name, font=F20M, fill=color if index == 1 else MUTED)
        draw.rounded_rectangle((x + 45, 565, x + 545, 636), 16, fill="#10253A", outline=color, width=2)
        draw.text((x + 68, 588), f"ALLOWED: {files[0]}{files[1].strip()}", font=F18M, fill=color)
    card(draw, (747, 370, 853, 594), fill="#091525", outline=GREEN, radius=28)
    center_x = 800
    zero_box = draw.textbbox((0, 0), "0", font=F58B)
    draw.text((center_x - (zero_box[2] - zero_box[0]) / 2, 418), "0", font=F58B, fill=GREEN)
    draw.text((762, 490), "OVERLAP", font=F18, fill=MUTED)
    check(draw, 800, 553, 20)
    footer(draw, "Disjoint scopes make parallel work safe and reviewable.", 2)
    return image


def lane(draw, y, title, path, color, progress):
    card(draw, (180, y, 1420, y + 190))
    draw.text((220, y + 28), title, font=F26B, fill=INK)
    draw.text((220, y + 70), path, font=F18M, fill=color)
    draw.rounded_rectangle((220, y + 120, 1270, y + 146), 13, fill="#07101F")
    draw.rounded_rectangle((220, y + 120, 220 + int(1050 * max(0.04, progress)), y + 146), 13, fill=color)
    check(draw, 1340, y + 132, 22) if progress >= 0.98 else draw.text((1305, y + 113), f"{int(progress * 100):02d}%", font=F22B, fill=color)


def parallel(progress):
    image = canvas("Parallel dispatch")
    draw = ImageDraw.Draw(image)
    center(draw, 132, "BOTH TASKS START BEFORE WAITING", F44B)
    center(draw, 190, "Independent work moves at the same time.", F26B, MUTED)
    lane(draw, 280, "Backend validation", "task-…-1  ·  backend/validate.js", BLUE, min(1, ease(progress * 1.22)))
    lane(draw, 505, "Frontend empty state", "task-…-2  ·  frontend/empty-state.js", PURPLE, min(1, ease(max(0, progress * 1.15 - 0.05))))
    footer(draw, "Two workstreams. One controlled workflow.", 3)
    return image


def result(draw, x, title, path, tests, color):
    card(draw, (x, 245, x + 610, 630), outline=color)
    check(draw, x + 54, 300, 23)
    draw.text((x + 92, 278), title, font=F26B, fill=INK)
    draw.text((x + 36, 355), "CHANGED FILE", font=F16, fill=MUTED)
    draw.text((x + 36, 390), path, font=F18M, fill=color)
    draw.line((x + 36, 440, x + 574, 440), fill=STROKE, width=2)
    draw.text((x + 36, 472), "WORKER CHECK", font=F16, fill=MUTED)
    draw.text((x + 36, 510), tests, font=F22B, fill=GREEN)
    draw.text((x + 36, 558), "DIFF RETURNED FOR REVIEW", font=F18, fill=INK)
    check(draw, x + 548, 570, 15)


def verified(_):
    image = canvas("Verification evidence")
    draw = ImageDraw.Draw(image)
    center(draw, 124, "VERIFIED RESULTS — NOT PROMISES", F44B)
    center(draw, 181, "Completion requires recorded checks and real file changes.", F26B, MUTED)
    result(draw, 120, "Backend complete", "backend/validate.js", "2 / 2 TESTS PASSED", BLUE)
    result(draw, 870, "Frontend complete", "frontend/empty-state.js", "1 / 1 TEST PASSED", PURPLE)
    card(draw, (485, 675, 1115, 770), fill="#103128", outline=GREEN, radius=25, width=3)
    center(draw, 691, "PRIMARY RECHECK: 3 / 3 PASSED", F30B, GREEN)
    center(draw, 733, "0 warnings · 0 out-of-scope files", F19B)
    footer(draw, "The primary agent verifies the returned work again.", 4)
    return image


def diff(draw, x, title, old, new, color):
    card(draw, (x, 238, x + 650, 660), fill="#0A1525", outline=color)
    draw.text((x + 32, 270), title, font=F22B, fill=INK)
    draw.text((x + 32, 312), "REVIEW-READY DIFF", font=F16, fill=color)
    draw.rounded_rectangle((x + 28, 360, x + 622, 440), 14, fill="#291722")
    draw.text((x + 48, 386), f"- {old}", font=F18M, fill=RED)
    draw.rounded_rectangle((x + 28, 458, x + 622, 555), 14, fill="#102A24")
    draw.text((x + 48, 485), f"+ {new}", font=F18M, fill=GREEN)
    draw.text((x + 32, 600), "Within declared scope", font=F18, fill=MUTED)
    check(draw, x + 585, 610, 15)


def review(_):
    image = canvas("Final diff review")
    draw = ImageDraw.Draw(image)
    center(draw, 123, "FINAL CONTROL STAYS WITH YOUR AGENT", F44B)
    center(draw, 181, "Every worker result comes back as an inspectable diff.", F26B, MUTED)
    diff(draw, 95, "backend/validate.js", "return true", "trim input; reject blank", BLUE)
    diff(draw, 855, "frontend/empty-state.js", 'return ""', "return approved empty-state copy", PURPLE)
    pill(draw, 486, 704, "2 REVIEW-READY DIFFS", GREEN, 300)
    pill(draw, 814, 704, "NO HIDDEN WRITES", BLUE, 290)
    footer(draw, "Delegate implementation. Keep architectural judgment and approval.", 5)
    return image


def outro(progress):
    image = canvas()
    draw = ImageDraw.Draw(image)
    size = int(148 + 20 * ease(progress))
    image.alpha_composite(LOGO.resize((size, size), Image.Resampling.LANCZOS), ((W - size) // 2, 92))
    center(draw, 305, "FROM REQUEST TO", F58B)
    center(draw, 375, "REVIEW-READY RESULT", F58B, BLUE)
    center(draw, 478, "2 parallel workstreams  ·  0 overlap  ·  3/3 checks passed", F26B)
    card(draw, (420, 570, 1180, 702), fill="#112D37", outline=GREEN, radius=28, width=3)
    center(draw, 595, "MCP CODER DELEGATOR", F36B)
    center(draw, 646, "$12 ON AGENSI", F30B, GREEN)
    center(draw, 760, "Delegate implementation. Keep control of the code.", F22B, MUTED)
    return image


SCENES = [intro, contracts, scopes, parallel, verified, review, outro]
BOUNDS = [0, 3, 8, 13, 18, 24, 28, 31]


def render_at(second):
    index = next(i for i in range(len(SCENES)) if second < BOUNDS[i + 1])
    start, end = BOUNDS[index], BOUNDS[index + 1]
    current = SCENES[index]((second - start) / (end - start))
    if index and second - start < 0.34:
        current = Image.blend(SCENES[index - 1](1.0), current, ease((second - start) / 0.34))
    return current.convert("RGB")


def main():
    command = ["ffmpeg", "-hide_banner", "-loglevel", "warning", "-y", "-f", "rawvideo", "-pix_fmt", "rgb24", "-s", f"{W}x{H}", "-r", str(FPS), "-i", "-", "-an", "-c:v", "libx264", "-preset", "medium", "-crf", "20", "-pix_fmt", "yuv420p", "-movflags", "+faststart", str(OUTPUT)]
    process = subprocess.Popen(command, stdin=subprocess.PIPE)
    assert process.stdin is not None
    for frame in range(DURATION * FPS):
        process.stdin.write(render_at(frame / FPS).tobytes())
    process.stdin.close()
    if process.wait() != 0:
        raise SystemExit("ffmpeg failed")
    print(OUTPUT)


if __name__ == "__main__":
    main()
