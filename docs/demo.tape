# docs/demo.tape — VHS script that records the README demo GIF.
#
# VHS renders this deterministically (no real Docker needed — it drives the
# built-in `-demo` backend). Generate the GIF with:
#
#     vhs docs/demo.tape          # writes docs/demo.gif
#
# Requirements: https://github.com/charmbracelet/vhs (vhs + ffmpeg + ttyd).
# See docs/README.md for install notes.

Output docs/demo.gif

Set Shell bash
Set FontSize 16
Set Width 1280
Set Height 720
Set Padding 18
Set TypingSpeed 70ms
Set Theme "TokyoNight"

# --- Off-camera: build once, then launch the demo backend. ---
Hide
Type "go build -o d9c . && clear"
Enter
Sleep 4s
Type "./d9c -demo"
Enter
Sleep 2s
Show

# --- Containers: browse the list. ---
Sleep 1500ms
Down
Down
Down
Sleep 1s
Up
Sleep 800ms

# --- Live CPU/MEM metrics (toggle the stats layout). ---
Type "s"
Sleep 2500ms
Type "s"
Sleep 800ms

# --- Live filter, then clear it. ---
Type "/web"
Sleep 1800ms
Enter
Sleep 1800ms
Type "/"
Backspace
Backspace
Backspace
Enter
Sleep 800ms

# --- Container logs, then back. ---
Type "l"
Sleep 2500ms
Escape
Sleep 800ms

# --- Tour the other sections via the command line. ---
Type ":images"
Enter
Sleep 1800ms
Type ":networks"
Enter
Sleep 1500ms
Type ":volumes"
Enter
Sleep 1500ms
Type ":compose"
Enter
Sleep 1800ms
Type ":hosts"
Enter
Sleep 2000ms

# --- Contextual help overlay. ---
Type "?"
Sleep 2500ms
Escape
Sleep 600ms

# --- Back to containers and quit. ---
Type ":containers"
Enter
Sleep 800ms
Type "q"
Sleep 1s
