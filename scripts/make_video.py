#!/usr/bin/env python3
"""
Generate a 30-second Matrix Reloaded themed video for VMSmith.
Agent Smith narrates the capabilities of VMSmith with falling Matrix rain.
"""

import random
import math
import numpy as np
from PIL import Image, ImageDraw, ImageFont
from moviepy import VideoClip, AudioClip, concatenate_videoclips

# === CONFIG ===
WIDTH, HEIGHT = 1920, 1080
FPS = 24
DURATION = 33  # seconds
BG_COLOR = (0, 0, 0)
MATRIX_GREEN = (0, 255, 65)
DIM_GREEN = (0, 120, 30)
FAINT_GREEN = (0, 60, 15)
BRIGHT_GREEN = (120, 255, 140)
WHITE = (255, 255, 255)

FONT_MONO = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"
FONT_MONO_BOLD = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf"

# Matrix rain characters
MATRIX_CHARS = list("abcdefghijklmnopqrstuvwxyz0123456789@#$%^&*(){}[]|;:<>VMSMITHvmsmith")

# === SCRIPT / TIMELINE ===
# Each entry: (start_time, end_time, text, style)
# style: "smith" for Agent Smith quotes, "feature" for VMSmith features, "title" for big text
SCRIPT = [
    # Opening - dramatic Agent Smith entrance
    (0.0,  3.5,  "Mr. Anderson...\nsurprised to see me?", "smith"),
    (4.0,  7.5,  "I've been watching your infrastructure.\nSo many machines... so much chaos.", "smith"),

    # Introduce VMSmith
    (8.0, 11.0,  "V M S M I T H", "title"),
    (8.0, 11.0,  "One binary to rule them all.", "subtitle"),

    # Purpose quote + features
    (11.5, 15.0, "It is purpose that created us.\nPurpose that connects us.\nPurpose... that drives us.", "smith"),

    # Features cascade
    (15.5, 18.0, ">>> VM LIFECYCLE\ncreate | start | stop | delete\nOne command. Total control.", "feature"),
    (18.5, 21.0, ">>> SNAPSHOTS\nCapture state. Rewind time.\nNo more \"it worked yesterday.\"", "feature"),
    (21.5, 24.0, ">>> PORTABLE IMAGES\nExport. Transfer. Deploy.\nqcow2 images across hosts.", "feature"),
    (24.5, 27.0, ">>> PORT FORWARDING\nNAT networking. iptables DNAT.\nExpose any VM service instantly.", "feature"),

    # Closing - Agent Smith
    (27.5, 30.0, "I am here because of you,\nMr. Anderson.", "smith"),
    (30.5, 33.0, "github.com/vmsmith/vmsmith", "title"),
    (30.5, 33.0, "CLI  |  REST API  |  Web GUI", "subtitle"),
]


class MatrixRain:
    """Generates the classic Matrix digital rain effect."""

    def __init__(self, width, height, num_columns=120):
        self.width = width
        self.height = height
        self.char_size = width // num_columns
        self.num_cols = num_columns
        self.num_rows = height // self.char_size + 2

        # Each column: current head position, speed, trail length
        self.columns = []
        for _ in range(self.num_cols):
            self.columns.append({
                'pos': random.uniform(-self.num_rows, 0),
                'speed': random.uniform(0.3, 1.2),
                'trail': random.randint(8, 25),
                'chars': [random.choice(MATRIX_CHARS) for _ in range(self.num_rows + 30)]
            })

        self.font = ImageFont.truetype(FONT_MONO, self.char_size - 2)

    def render(self, t):
        """Render matrix rain at time t, return PIL Image."""
        img = Image.new('RGB', (self.width, self.height), BG_COLOR)
        draw = ImageDraw.Draw(img)

        for col_idx, col in enumerate(self.columns):
            head = col['pos'] + col['speed'] * t * 15
            # Wrap around
            if head > self.num_rows + col['trail']:
                col['pos'] = random.uniform(-self.num_rows, -5)
                col['speed'] = random.uniform(0.3, 1.2)
                col['trail'] = random.randint(8, 25)
                col['chars'] = [random.choice(MATRIX_CHARS) for _ in range(self.num_rows + 30)]
                head = col['pos'] + col['speed'] * t * 15

            x = col_idx * self.char_size

            for row in range(self.num_rows):
                dist_from_head = head - row
                if dist_from_head < 0 or dist_from_head > col['trail']:
                    continue

                y = row * self.char_size

                # Randomly change characters occasionally
                if random.random() < 0.02:
                    col['chars'][row % len(col['chars'])] = random.choice(MATRIX_CHARS)

                char = col['chars'][row % len(col['chars'])]

                # Color based on distance from head
                if dist_from_head < 1:
                    color = WHITE  # brightest at head
                elif dist_from_head < 3:
                    color = BRIGHT_GREEN
                elif dist_from_head < col['trail'] * 0.5:
                    alpha = 1.0 - (dist_from_head / col['trail'])
                    color = (0, int(255 * alpha), int(65 * alpha))
                else:
                    alpha = max(0.1, 1.0 - (dist_from_head / col['trail']))
                    color = (0, int(80 * alpha), int(20 * alpha))

                draw.text((x, y), char, fill=color, font=self.font)

        return img


def ease_in_out(t, duration=0.5):
    """Smooth fade in/out factor (0-1)."""
    if t < duration:
        return t / duration
    return 1.0


def render_text_block(draw, text, style, t_local, t_duration, width, height):
    """Render a styled text block onto the frame."""
    # Calculate fade
    fade_dur = 0.4
    if t_local < fade_dur:
        alpha = t_local / fade_dur
    elif t_local > t_duration - fade_dur:
        alpha = max(0, (t_duration - t_local) / fade_dur)
    else:
        alpha = 1.0

    if alpha <= 0:
        return

    lines = text.split('\n')

    if style == "title":
        font_size = 80
        font = ImageFont.truetype(FONT_MONO_BOLD, font_size)
        color = MATRIX_GREEN
        y_base = height // 2 - 80
    elif style == "subtitle":
        font_size = 36
        font = ImageFont.truetype(FONT_MONO, font_size)
        color = DIM_GREEN
        y_base = height // 2 + 40
    elif style == "smith":
        font_size = 44
        font = ImageFont.truetype(FONT_MONO_BOLD, font_size)
        color = MATRIX_GREEN
        y_base = height // 2 - (len(lines) * 55) // 2
    elif style == "feature":
        font_size = 40
        font = ImageFont.truetype(FONT_MONO_BOLD, font_size)
        color = MATRIX_GREEN
        y_base = height // 2 - (len(lines) * 52) // 2
    else:
        font_size = 36
        font = ImageFont.truetype(FONT_MONO, font_size)
        color = MATRIX_GREEN
        y_base = height // 2

    # Apply alpha to color
    color = tuple(int(c * alpha) for c in color)

    for i, line in enumerate(lines):
        if style == "feature" and i == 0:
            # First line of feature is the header - use bright green
            line_color = tuple(int(c * alpha) for c in BRIGHT_GREEN)
        elif style == "smith":
            line_color = tuple(int(c * alpha) for c in (200, 255, 200))
        else:
            line_color = color

        # Typewriter effect for smith style
        if style == "smith":
            chars_to_show = int(len(line) * min(1.0, t_local * 3.0 / max(len(line) * 0.04, 0.5)))
            if i > 0:
                delay = i * 0.8
                if t_local < delay:
                    continue
                chars_to_show = int(len(line) * min(1.0, (t_local - delay) * 3.0 / max(len(line) * 0.04, 0.5)))
            visible_line = line[:chars_to_show]
        else:
            visible_line = line

        # Center text
        bbox = draw.textbbox((0, 0), visible_line, font=font)
        tw = bbox[2] - bbox[0]
        x = (width - tw) // 2
        y = y_base + i * (font_size + 12)

        # Draw shadow for readability
        shadow_color = (0, 0, 0)
        for dx, dy in [(-2, -2), (-2, 2), (2, -2), (2, 2), (-3, 0), (3, 0), (0, -3), (0, 3)]:
            draw.text((x + dx, y + dy), visible_line, fill=shadow_color, font=font)

        # Draw glow
        glow_color = tuple(max(0, min(255, c // 3)) for c in line_color)
        for dx, dy in [(-1, -1), (-1, 1), (1, -1), (1, 1)]:
            draw.text((x + dx, y + dy), visible_line, fill=glow_color, font=font)

        draw.text((x, y), visible_line, fill=line_color, font=font)


def add_scanlines(img, alpha=0.08):
    """Add subtle CRT scanline effect."""
    draw = ImageDraw.Draw(img)
    for y in range(0, img.height, 3):
        draw.line([(0, y), (img.width, y)], fill=(0, 0, 0), width=1)


def add_vignette(img, strength=0.6):
    """Add dark vignette around edges."""
    arr = np.array(img, dtype=np.float64)
    h, w = arr.shape[:2]
    cx, cy = w / 2, h / 2
    max_dist = math.sqrt(cx**2 + cy**2)

    Y, X = np.ogrid[:h, :w]
    dist = np.sqrt((X - cx)**2 + (Y - cy)**2) / max_dist
    vignette = 1.0 - dist * strength
    vignette = np.clip(vignette, 0, 1)

    arr *= vignette[:, :, np.newaxis]
    return Image.fromarray(arr.astype(np.uint8))


# === MAIN VIDEO GENERATION ===
print("Initializing Matrix rain...")
rain = MatrixRain(WIDTH, HEIGHT, num_columns=110)

# Pre-seed the rain so it's already falling at t=0
for col in rain.columns:
    col['pos'] = random.uniform(-30, 20)

frame_count = [0]

def make_frame(t):
    """Generate a single video frame at time t."""
    frame_count[0] += 1
    if frame_count[0] % (FPS * 2) == 0:
        print(f"  Rendering t={t:.1f}s / {DURATION}s ...")

    # 1. Render matrix rain background
    img = rain.render(t)

    # 2. Darken rain where text will appear (center region)
    arr = np.array(img, dtype=np.float64)
    # Create a darkening mask for center area
    h, w = arr.shape[:2]
    center_mask = np.ones((h, w), dtype=np.float64)
    y_center = h // 2
    for y in range(h):
        dist = abs(y - y_center) / (h * 0.3)
        if dist < 1.0:
            center_mask[y, :] = 0.15 + 0.85 * dist

    # Only darken when text is showing
    has_text = any(s <= t <= e for s, e, _, _ in SCRIPT)
    if has_text:
        arr *= center_mask[:, :, np.newaxis]

    img = Image.fromarray(arr.astype(np.uint8))
    draw = ImageDraw.Draw(img)

    # 3. Render active text blocks
    for start, end, text, style in SCRIPT:
        if start <= t <= end:
            t_local = t - start
            t_duration = end - start
            render_text_block(draw, text, style, t_local, t_duration, WIDTH, HEIGHT)

    # 4. Post-processing
    add_scanlines(img, alpha=0.06)
    img = add_vignette(img, strength=0.5)

    # 5. Subtle green color grade on everything
    arr = np.array(img, dtype=np.float64)
    arr[:, :, 1] = np.clip(arr[:, :, 1] * 1.1, 0, 255)  # boost green slightly
    arr[:, :, 0] = arr[:, :, 0] * 0.85  # reduce red
    arr[:, :, 2] = arr[:, :, 2] * 0.85  # reduce blue

    return arr.astype(np.uint8)


print("Generating video frames...")
video = VideoClip(make_frame, duration=DURATION)

output_path = "/home/user/VMSmith/vmsmith-matrix.mp4"
print(f"Writing video to {output_path} ...")
video.write_videofile(
    output_path,
    fps=FPS,
    codec='libx264',
    preset='medium',
    bitrate='5000k',
    audio=False,
    logger='bar',
)

print(f"\nDone! Video saved to: {output_path}")
print(f"Duration: {DURATION}s, Resolution: {WIDTH}x{HEIGHT}, FPS: {FPS}")
