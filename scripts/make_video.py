#!/usr/bin/env python3
"""
Generate a 30-second Matrix Reloaded themed video for VMSmith.
Agent Smith narrates the capabilities of VMSmith with:
- Matrix digital rain
- Agent Smith silhouette walking animation
- Text-to-speech narration (espeak-ng)
- CRT scanlines, vignette, green color grading
"""

import random
import math
import os
import subprocess
import struct
import wave
import numpy as np
from PIL import Image, ImageDraw, ImageFont
from moviepy import VideoClip, AudioFileClip, CompositeAudioClip, concatenate_audioclips

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

MATRIX_CHARS = list("abcdefghijklmnopqrstuvwxyz0123456789@#$%^&*(){}[]|;:<>VMSMITHvmsmith")

OUTPUT_DIR = "/home/user/VMSmith"
AUDIO_DIR = "/tmp/vmsmith_audio"

# === SCRIPT / TIMELINE ===
SCRIPT = [
    (0.0,  3.5,  "Mr. Anderson...\nsurprised to see me?", "smith"),
    (4.0,  7.5,  "I've been watching your infrastructure.\nSo many machines... so much chaos.", "smith"),
    (8.0, 11.0,  "V M S M I T H", "title"),
    (8.0, 11.0,  "One binary to rule them all.", "subtitle"),
    (11.5, 15.0, "It is purpose that created us.\nPurpose that connects us.\nPurpose... that drives us.", "smith"),
    (15.5, 18.0, ">>> VM LIFECYCLE\ncreate | start | stop | delete\nOne command. Total control.", "feature"),
    (18.5, 21.0, ">>> SNAPSHOTS\nCapture state. Rewind time.\nNo more \"it worked yesterday.\"", "feature"),
    (21.5, 24.0, ">>> PORTABLE IMAGES\nExport. Transfer. Deploy.\nqcow2 images across hosts.", "feature"),
    (24.5, 27.0, ">>> PORT FORWARDING\nNAT networking. iptables DNAT.\nExpose any VM service instantly.", "feature"),
    (27.5, 30.0, "I am here because of you,\nMr. Anderson.", "smith"),
    (30.5, 33.0, "github.com/vmsmith/vmsmith", "title"),
    (30.5, 33.0, "CLI  |  REST API  |  Web GUI", "subtitle"),
]

# Voice lines for TTS - (start_time, text_to_speak)
VOICE_LINES = [
    (0.0,   "Mr. Anderson... surprised to see me?"),
    (4.0,   "I've been watching your infrastructure. So many machines. So much chaos."),
    (8.0,   "VM Smith."),
    (11.5,  "It is purpose that created us. Purpose that connects us. Purpose, that drives us."),
    (15.5,  "VM lifecycle. Create. Start. Stop. Delete. One command. Total control."),
    (18.5,  "Snapshots. Capture state. Rewind time."),
    (21.5,  "Portable images. Export. Transfer. Deploy."),
    (24.5,  "Port forwarding. NAT networking. Expose any VM service, instantly."),
    (27.5,  "I am here because of you, Mr. Anderson."),
    (30.5,  "VM Smith. CLI. REST API. Web GUI."),
]


# ============================================================
# AGENT SMITH SILHOUETTE
# ============================================================
class AgentSmithSilhouette:
    """Draws a walking silhouette of a man in a suit (Agent Smith style)."""

    def __init__(self):
        self.base_height = 500  # pixels tall
        self.base_width = 200

    def draw_figure(self, draw, cx, cy, scale=1.0, walk_phase=0.0, alpha=1.0):
        """Draw Agent Smith silhouette at center (cx, cy) with walk animation.

        walk_phase: 0-1 for walk cycle
        alpha: 0-1 for transparency (simulated with color dimming)
        """
        s = scale
        color_val = int(20 * alpha)
        body_color = (0, color_val, int(color_val * 0.3))
        outline_color = (0, int(50 * alpha), int(15 * alpha))

        # Walk animation - leg and arm swing
        swing = math.sin(walk_phase * 2 * math.pi) * 15 * s

        # HEAD (oval)
        head_r = int(28 * s)
        head_cy = cy - int(210 * s)
        draw.ellipse(
            [cx - head_r, head_cy - int(head_r * 1.2),
             cx + head_r, head_cy + int(head_r * 1.0)],
            fill=body_color, outline=outline_color
        )

        # SHOULDERS / TORSO (suit jacket - trapezoid)
        shoulder_w = int(70 * s)
        waist_w = int(50 * s)
        shoulder_y = cy - int(175 * s)
        waist_y = cy - int(50 * s)

        # Suit jacket body
        jacket = [
            (cx - shoulder_w, shoulder_y),
            (cx + shoulder_w, shoulder_y),
            (cx + waist_w, waist_y),
            (cx - waist_w, waist_y),
        ]
        draw.polygon(jacket, fill=body_color, outline=outline_color)

        # Suit collar / tie hint (thin line down center)
        tie_color = (0, int(35 * alpha), int(10 * alpha))
        draw.line([(cx, shoulder_y), (cx, waist_y - int(10 * s))],
                  fill=tie_color, width=max(1, int(3 * s)))

        # ARMS - swing with walk
        arm_length = int(100 * s)
        # Left arm
        left_arm_end_x = cx - shoulder_w - int(10 * s)
        left_arm_end_y = shoulder_y + arm_length + int(swing)
        draw.line([(cx - shoulder_w + int(5 * s), shoulder_y + int(10 * s)),
                   (left_arm_end_x, left_arm_end_y)],
                  fill=outline_color, width=max(2, int(8 * s)))

        # Right arm
        right_arm_end_x = cx + shoulder_w + int(10 * s)
        right_arm_end_y = shoulder_y + arm_length - int(swing)
        draw.line([(cx + shoulder_w - int(5 * s), shoulder_y + int(10 * s)),
                   (right_arm_end_x, right_arm_end_y)],
                  fill=outline_color, width=max(2, int(8 * s)))

        # LEGS - suit pants
        hip_y = waist_y
        leg_length = int(160 * s)
        leg_w = max(2, int(10 * s))

        # Left leg
        left_foot_x = cx - int(25 * s) - int(swing * 0.8)
        left_foot_y = hip_y + leg_length
        draw.line([(cx - int(15 * s), hip_y), (left_foot_x, left_foot_y)],
                  fill=outline_color, width=leg_w)

        # Right leg
        right_foot_x = cx + int(25 * s) + int(swing * 0.8)
        right_foot_y = hip_y + leg_length
        draw.line([(cx + int(15 * s), hip_y), (right_foot_x, right_foot_y)],
                  fill=outline_color, width=leg_w)

        # SUNGLASSES (iconic Agent Smith detail)
        glasses_y = head_cy - int(5 * s)
        glasses_w = int(18 * s)
        glasses_h = int(8 * s)
        glasses_color = (0, int(60 * alpha), int(20 * alpha))
        # Left lens
        draw.rectangle([cx - int(22 * s), glasses_y - glasses_h,
                        cx - int(22 * s) + glasses_w, glasses_y + glasses_h],
                       fill=glasses_color)
        # Right lens
        draw.rectangle([cx + int(4 * s), glasses_y - glasses_h,
                        cx + int(4 * s) + glasses_w, glasses_y + glasses_h],
                       fill=glasses_color)
        # Bridge
        draw.line([(cx - int(4 * s), glasses_y),
                   (cx + int(4 * s), glasses_y)],
                  fill=glasses_color, width=max(1, int(2 * s)))


# ============================================================
# AGENT SMITH ANIMATION SCENES
# ============================================================
# (start_time, end_time, animation_type, params)
# animation_type: "walk_in", "stand", "walk_across", "fade_in", "fade_out"
SMITH_SCENES = [
    # Opening: Smith walks in from right, stops center-right
    (0.0,  3.5,  "walk_in_right", {"start_x": WIDTH + 100, "end_x": WIDTH * 0.75, "y": HEIGHT * 0.55, "scale": 0.8}),
    # Watching infrastructure - slight sway
    (4.0,  7.5,  "stand_sway", {"x": WIDTH * 0.75, "y": HEIGHT * 0.55, "scale": 0.8}),
    # Title reveal - Smith fades slightly, moves left
    (8.0,  11.0, "walk_slow", {"start_x": WIDTH * 0.75, "end_x": WIDTH * 0.2, "y": HEIGHT * 0.55, "scale": 0.7}),
    # Purpose speech - stands on left
    (11.5, 15.0, "stand_sway", {"x": WIDTH * 0.2, "y": HEIGHT * 0.55, "scale": 0.75}),
    # Features - Smith walks across behind text
    (15.5, 27.0, "walk_across", {"start_x": WIDTH * 0.15, "end_x": WIDTH * 0.85, "y": HEIGHT * 0.55, "scale": 0.65}),
    # Closing - Smith walks to center
    (27.5, 30.0, "walk_to_center", {"start_x": WIDTH * 0.85, "end_x": WIDTH * 0.5, "y": HEIGHT * 0.55, "scale": 0.85}),
    # Repo URL - Smith fades out
    (30.5, 33.0, "fade_out", {"x": WIDTH * 0.5, "y": HEIGHT * 0.55, "scale": 0.85}),
]


smith = AgentSmithSilhouette()


def render_smith(draw, t):
    """Render Agent Smith silhouette based on current time."""
    for start, end, anim_type, params in SMITH_SCENES:
        if not (start <= t <= end):
            continue

        progress = (t - start) / (end - start)
        walk_speed = t * 1.5  # walk cycle speed

        if anim_type == "walk_in_right":
            x = params['start_x'] + (params['end_x'] - params['start_x']) * ease_smooth(progress)
            smith.draw_figure(draw, int(x), int(params['y']),
                            scale=params['scale'], walk_phase=walk_speed, alpha=1.0)

        elif anim_type == "stand_sway":
            sway = math.sin(t * 0.5) * 3
            smith.draw_figure(draw, int(params['x'] + sway), int(params['y']),
                            scale=params['scale'], walk_phase=0, alpha=0.9)

        elif anim_type == "walk_slow":
            x = params['start_x'] + (params['end_x'] - params['start_x']) * ease_smooth(progress)
            smith.draw_figure(draw, int(x), int(params['y']),
                            scale=params['scale'], walk_phase=walk_speed * 0.5, alpha=0.8)

        elif anim_type == "walk_across":
            x = params['start_x'] + (params['end_x'] - params['start_x']) * progress
            smith.draw_figure(draw, int(x), int(params['y']),
                            scale=params['scale'], walk_phase=walk_speed, alpha=0.5)

        elif anim_type == "walk_to_center":
            x = params['start_x'] + (params['end_x'] - params['start_x']) * ease_smooth(progress)
            smith.draw_figure(draw, int(x), int(params['y']),
                            scale=params['scale'], walk_phase=walk_speed, alpha=1.0)

        elif anim_type == "fade_out":
            alpha = 1.0 - progress
            smith.draw_figure(draw, int(params['x']), int(params['y']),
                            scale=params['scale'], walk_phase=0, alpha=alpha)


def ease_smooth(t):
    """Smooth ease in/out."""
    return t * t * (3 - 2 * t)


# ============================================================
# AUDIO GENERATION
# ============================================================
def generate_audio():
    """Generate TTS audio files for each voice line and combine them."""
    os.makedirs(AUDIO_DIR, exist_ok=True)

    # Voice settings for Agent Smith: deep, slow, deliberate
    # en-gb-x-rp = Received Pronunciation (formal British, closest to Hugo Weaving's delivery)
    voice = "en-gb-x-rp"
    speed = 130   # words per minute (slow, deliberate)
    pitch = 20    # lower pitch for deeper voice
    amplitude = 180

    audio_files = []

    for i, (start_time, text) in enumerate(VOICE_LINES):
        wav_path = f"{AUDIO_DIR}/line_{i:02d}.wav"
        cmd = [
            "espeak-ng",
            "-v", voice,
            "-s", str(speed),
            "-p", str(pitch),
            "-a", str(amplitude),
            "-w", wav_path,
            text
        ]
        print(f"  Generating voice line {i}: {text[:50]}...")
        subprocess.run(cmd, check=True, capture_output=True)
        audio_files.append((start_time, wav_path))

    return audio_files


def create_combined_audio(audio_files):
    """Combine individual voice lines into a single audio track with proper timing."""
    sample_rate = 22050
    total_samples = int(DURATION * sample_rate)
    combined = np.zeros(total_samples, dtype=np.float64)

    for start_time, wav_path in audio_files:
        # Read wav file
        with wave.open(wav_path, 'rb') as wf:
            n_channels = wf.getnchannels()
            sampwidth = wf.getsampwidth()
            framerate = wf.getframerate()
            n_frames = wf.getnframes()
            raw_data = wf.readframes(n_frames)

        # Convert to numpy array
        if sampwidth == 2:
            samples = np.frombuffer(raw_data, dtype=np.int16).astype(np.float64) / 32768.0
        elif sampwidth == 1:
            samples = (np.frombuffer(raw_data, dtype=np.uint8).astype(np.float64) - 128) / 128.0
        else:
            continue

        # If stereo, take first channel
        if n_channels == 2:
            samples = samples[::2]

        # Resample if needed
        if framerate != sample_rate:
            ratio = sample_rate / framerate
            new_len = int(len(samples) * ratio)
            indices = np.linspace(0, len(samples) - 1, new_len)
            samples = np.interp(indices, np.arange(len(samples)), samples)

        # Place at correct position
        start_sample = int(start_time * sample_rate)
        end_sample = min(start_sample + len(samples), total_samples)
        samples_to_place = end_sample - start_sample
        if samples_to_place > 0:
            combined[start_sample:end_sample] = samples[:samples_to_place]

    # Normalize
    max_val = np.max(np.abs(combined))
    if max_val > 0:
        combined = combined / max_val * 0.9

    # Add a subtle ambient hum (matrix-like)
    t_arr = np.linspace(0, DURATION, total_samples)
    hum = np.sin(2 * np.pi * 60 * t_arr) * 0.03  # 60Hz hum
    hum += np.sin(2 * np.pi * 120 * t_arr) * 0.015  # harmonic
    # Slowly modulate the hum
    hum *= (1.0 + 0.3 * np.sin(2 * np.pi * 0.1 * t_arr))

    combined = combined * 0.85 + hum

    # Save combined audio
    combined_path = f"{AUDIO_DIR}/combined.wav"
    combined_int16 = (np.clip(combined, -1, 1) * 32767).astype(np.int16)

    with wave.open(combined_path, 'wb') as wf:
        wf.setnchannels(1)
        wf.setsampwidth(2)
        wf.setframerate(sample_rate)
        wf.writeframes(combined_int16.tobytes())

    print(f"  Combined audio saved: {combined_path}")
    return combined_path


# ============================================================
# MATRIX RAIN (same as before but improved)
# ============================================================
class MatrixRain:
    def __init__(self, width, height, num_columns=120):
        self.width = width
        self.height = height
        self.char_size = width // num_columns
        self.num_cols = num_columns
        self.num_rows = height // self.char_size + 2
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
        img = Image.new('RGB', (self.width, self.height), BG_COLOR)
        draw = ImageDraw.Draw(img)
        for col_idx, col in enumerate(self.columns):
            head = col['pos'] + col['speed'] * t * 15
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
                if random.random() < 0.02:
                    col['chars'][row % len(col['chars'])] = random.choice(MATRIX_CHARS)
                char = col['chars'][row % len(col['chars'])]
                if dist_from_head < 1:
                    color = WHITE
                elif dist_from_head < 3:
                    color = BRIGHT_GREEN
                elif dist_from_head < col['trail'] * 0.5:
                    a = 1.0 - (dist_from_head / col['trail'])
                    color = (0, int(255 * a), int(65 * a))
                else:
                    a = max(0.1, 1.0 - (dist_from_head / col['trail']))
                    color = (0, int(80 * a), int(20 * a))
                draw.text((x, y), char, fill=color, font=self.font)
        return img


# ============================================================
# TEXT RENDERING
# ============================================================
def render_text_block(draw, text, style, t_local, t_duration, width, height):
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
    color = tuple(int(c * alpha) for c in color)

    for i, line in enumerate(lines):
        if style == "feature" and i == 0:
            line_color = tuple(int(c * alpha) for c in BRIGHT_GREEN)
        elif style == "smith":
            line_color = tuple(int(c * alpha) for c in (200, 255, 200))
        else:
            line_color = color
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
        bbox = draw.textbbox((0, 0), visible_line, font=font)
        tw = bbox[2] - bbox[0]
        x = (width - tw) // 2
        y = y_base + i * (font_size + 12)
        shadow_color = (0, 0, 0)
        for dx, dy in [(-2, -2), (-2, 2), (2, -2), (2, 2), (-3, 0), (3, 0), (0, -3), (0, 3)]:
            draw.text((x + dx, y + dy), visible_line, fill=shadow_color, font=font)
        glow_color = tuple(max(0, min(255, c // 3)) for c in line_color)
        for dx, dy in [(-1, -1), (-1, 1), (1, -1), (1, 1)]:
            draw.text((x + dx, y + dy), visible_line, fill=glow_color, font=font)
        draw.text((x, y), visible_line, fill=line_color, font=font)


def add_scanlines(img, alpha=0.08):
    draw = ImageDraw.Draw(img)
    for y in range(0, img.height, 3):
        draw.line([(0, y), (img.width, y)], fill=(0, 0, 0), width=1)


def add_vignette(img, strength=0.6):
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


# ============================================================
# MAIN
# ============================================================
print("=" * 60)
print("VMSmith Matrix Video Generator (with audio + silhouette)")
print("=" * 60)

# Step 1: Generate audio
print("\n[1/3] Generating voice audio with espeak-ng...")
audio_files = generate_audio()
combined_audio_path = create_combined_audio(audio_files)

# Step 2: Generate video frames
print("\n[2/3] Generating video frames...")
rain = MatrixRain(WIDTH, HEIGHT, num_columns=110)
for col in rain.columns:
    col['pos'] = random.uniform(-30, 20)

frame_count = [0]

def make_frame(t):
    frame_count[0] += 1
    if frame_count[0] % (FPS * 2) == 0:
        print(f"  Rendering t={t:.1f}s / {DURATION}s ...")

    # 1. Matrix rain background
    img = rain.render(t)

    # 2. Darken center for text readability
    arr = np.array(img, dtype=np.float64)
    h, w = arr.shape[:2]
    center_mask = np.ones((h, w), dtype=np.float64)
    y_center = h // 2
    for y in range(h):
        dist = abs(y - y_center) / (h * 0.3)
        if dist < 1.0:
            center_mask[y, :] = 0.15 + 0.85 * dist
    has_text = any(s <= t <= e for s, e, _, _ in SCRIPT)
    if has_text:
        arr *= center_mask[:, :, np.newaxis]
    img = Image.fromarray(arr.astype(np.uint8))
    draw = ImageDraw.Draw(img)

    # 3. Draw Agent Smith silhouette
    render_smith(draw, t)

    # 4. Render text overlays
    for start, end, text, style in SCRIPT:
        if start <= t <= end:
            t_local = t - start
            t_duration = end - start
            render_text_block(draw, text, style, t_local, t_duration, WIDTH, HEIGHT)

    # 5. Post-processing
    add_scanlines(img, alpha=0.06)
    img = add_vignette(img, strength=0.5)

    # 6. Green color grade
    arr = np.array(img, dtype=np.float64)
    arr[:, :, 1] = np.clip(arr[:, :, 1] * 1.1, 0, 255)
    arr[:, :, 0] = arr[:, :, 0] * 0.85
    arr[:, :, 2] = arr[:, :, 2] * 0.85

    return arr.astype(np.uint8)


# Step 3: Compose final video with audio
print("\n[3/3] Composing final video with audio...")
video = VideoClip(make_frame, duration=DURATION)

# Attach audio
audio = AudioFileClip(combined_audio_path)
# Ensure audio matches video duration
if audio.duration < DURATION:
    # Pad with silence (audio is already the right duration from our generation)
    pass
video = video.with_audio(audio)

output_path = f"{OUTPUT_DIR}/vmsmith-matrix.mp4"
print(f"Writing final video to {output_path} ...")
video.write_videofile(
    output_path,
    fps=FPS,
    codec='libx264',
    audio_codec='aac',
    preset='medium',
    bitrate='5000k',
    logger='bar',
)

print(f"\nDone! Video saved to: {output_path}")
print(f"Duration: {DURATION}s, Resolution: {WIDTH}x{HEIGHT}, FPS: {FPS}")
print("Features: Matrix rain + Agent Smith silhouette + TTS narration + ambient hum")
