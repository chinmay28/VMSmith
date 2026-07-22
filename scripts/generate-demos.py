#!/usr/bin/env python3
"""Generate the animated SVG terminal demos embedded in the README
(roadmap 6.3.4).

The demos are self-contained SVGs animated with pure CSS keyframes — no
scripts — so they render (and loop) inside GitHub's sanitized <img>
context. Each "recording" is a scripted terminal session defined below;
re-run this script after changing a session:

    python3 scripts/generate-demos.py
"""

import html
import os

OUT_DIR = os.path.join(os.path.dirname(__file__), "..", "docs", "demos")

# Colors (terminal chrome + text)
BG = "#0d1117"
CHROME = "#161b22"
BORDER = "#30363d"
FG = "#e6edf3"
DIM = "#8b949e"
GREEN = "#3fb950"
BLUE = "#58a6ff"
YELLOW = "#d29922"

LINE_HEIGHT = 22
FONT_SIZE = 14
PAD_X = 18
PAD_TOP = 56
CHAR_SECONDS = 0.9  # seconds allotted per line reveal
HOLD_SECONDS = 4.0  # trailing hold before the loop restarts


def esc(s: str) -> str:
    return html.escape(s, quote=True)


def render(session, width=760, title="vmsmith"):
    """Render a session (list of (style, text) lines) into an SVG string."""
    n = len(session)
    total = n * CHAR_SECONDS + HOLD_SECONDS
    height = PAD_TOP + LINE_HEIGHT * n + 24

    styles = [
        f".term-line{{font-family:'SFMono-Regular',Consolas,'Liberation Mono',Menlo,monospace;font-size:{FONT_SIZE}px;white-space:pre;}}"
    ]
    body = []

    for i, (kind, text) in enumerate(session):
        t_show = i * CHAR_SECONDS
        p_show = 100.0 * t_show / total
        # Reveal window: hidden until its slot, visible through the hold,
        # hidden again as the loop restarts.
        styles.append(
            f"@keyframes reveal-{i}{{0%{{opacity:0}}{p_show:.2f}%{{opacity:0}}{min(p_show + 0.6, 99.0):.2f}%{{opacity:1}}99.4%{{opacity:1}}100%{{opacity:0}}}}"
            f".line-{i}{{animation:reveal-{i} {total:.1f}s linear infinite;}}"
        )
        y = PAD_TOP + LINE_HEIGHT * (i + 1) - 6

        if kind == "cmd":
            body.append(
                f'<text class="term-line line-{i}" x="{PAD_X}" y="{y}">'
                f'<tspan fill="{GREEN}">$</tspan> <tspan fill="{FG}">{esc(text)}</tspan></text>'
            )
        elif kind == "out":
            body.append(
                f'<text class="term-line line-{i}" x="{PAD_X}" y="{y}" fill="{DIM}">{esc(text)}</text>'
            )
        elif kind == "hi":
            body.append(
                f'<text class="term-line line-{i}" x="{PAD_X}" y="{y}" fill="{BLUE}">{esc(text)}</text>'
            )
        elif kind == "ok":
            body.append(
                f'<text class="term-line line-{i}" x="{PAD_X}" y="{y}" fill="{GREEN}">{esc(text)}</text>'
            )
        elif kind == "warn":
            body.append(
                f'<text class="term-line line-{i}" x="{PAD_X}" y="{y}" fill="{YELLOW}">{esc(text)}</text>'
            )

    style_block = "".join(styles)
    lines_block = "".join(body)
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">
  <style>{style_block}</style>
  <rect width="{width}" height="{height}" rx="10" fill="{BG}" stroke="{BORDER}"/>
  <rect width="{width}" height="36" rx="10" fill="{CHROME}"/>
  <rect y="26" width="{width}" height="10" fill="{CHROME}"/>
  <circle cx="20" cy="18" r="6" fill="#ff5f57"/>
  <circle cx="42" cy="18" r="6" fill="#febc2e"/>
  <circle cx="64" cy="18" r="6" fill="#28c840"/>
  <text x="{width / 2}" y="23" text-anchor="middle" fill="{DIM}" font-family="system-ui,sans-serif" font-size="13">{esc(title)}</text>
{lines_block}
</svg>
"""


VM_LIFECYCLE = [
    ("cmd", 'vmsmith vm create web-01 --image rocky9 --cpus 2 --ram 2048 --ssh-key "$(cat ~/.ssh/id_ed25519.pub)"'),
    ("out", "VM created successfully:"),
    ("out", "  ID:    vm-1741234567890"),
    ("out", "  Name:  web-01"),
    ("hi",  "  IP:    192.168.100.10   (static, ready before first boot)"),
    ("cmd", "vmsmith vm list"),
    ("out", "ID                 NAME    STATE    IP              CPUS  RAM_MB"),
    ("out", "vm-1741234567890   web-01  running  192.168.100.10  2     2048"),
    ("cmd", "vmsmith snapshot create vm-1741234567890 --name before-upgrade --tag audit"),
    ("ok",  "Snapshot 'before-upgrade' created"),
    ("cmd", "ssh root@192.168.100.10"),
    ("hi",  "[root@web-01 ~]#  █"),
]

FLEET_OPS = [
    ("cmd", "vmsmith vm list --tag prod --sort ip"),
    ("out", "ID                 NAME    STATE    IP              TAGS"),
    ("out", "vm-1741234567890   web-01  running  192.168.100.10  prod,web"),
    ("out", "vm-1741234568111   web-02  running  192.168.100.11  prod,web"),
    ("out", "vm-1741234568222   db-01   running  192.168.100.12  prod,db"),
    ("cmd", "vmsmith schedule create --name nightly-snap --tag prod --action snapshot --cron \"0 0 2 * * *\" --retention 7"),
    ("ok",  "Schedule created: sched-1741234570000 (next fire: 02:00:00)"),
    ("cmd", "vmsmith vm restart --all --tag prod"),
    ("out", "Restarted vm-1741234567890 (web-01)"),
    ("out", "Restarted vm-1741234568111 (web-02)"),
    ("out", "Restarted vm-1741234568222 (db-01)"),
    ("cmd", "vmsmith host stats"),
    ("out", "RESOURCE     USED     TOTAL    PERCENT"),
    ("out", "VMs          3        -        -"),
    ("out", "CPU          23%      100%     23%"),
    ("out", "RAM          6.2 GB   32 GB    19%"),
]


def main():
    os.makedirs(OUT_DIR, exist_ok=True)
    demos = {
        "vm-lifecycle.svg": render(VM_LIFECYCLE, title="vmsmith — create, snapshot, ssh"),
        "fleet-ops.svg": render(FLEET_OPS, title="vmsmith — fleet operations"),
    }
    for name, svg in demos.items():
        path = os.path.join(OUT_DIR, name)
        with open(path, "w") as f:
            f.write(svg)
        print(f"wrote {os.path.relpath(path)}")


if __name__ == "__main__":
    main()
