# Record a crisp, 100% sanitized TUI demo GIF for README.md
# Local usage:
#   go build -o ./aiswap-demo ./scripts/demo && vhs scripts/demo.tape && rm -f ./aiswap-demo

Output docs/preview.gif

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 14
Set Width 1260
Set Height 640
Set Padding 16
Set Framerate 20
Set Theme "TokyoNight"
Set WindowBar Colorful

Hide
Type "./aiswap-demo"
Enter
Sleep 1s
Show

# 1. Panoramic 3-column All view (Claude Code / Codex CLI / Antigravity)
Sleep 2s

# Move cursor and switch Claude Code slot #1 -> #2 live
Type "j"
Sleep 600ms
Enter
Sleep 1.8s

# Toggle Watch mode badge
Type "w"
Sleep 1.2s

# 2. Live available models grouped by shared 5h/7d quota pools
Type "6"
Sleep 2.5s

# 3. Offline token usage stats with threshold-colored daily bars
Type "5"
Sleep 2.5s

# Return to overview before loop repeats
Type "1"
Sleep 1.5s

Hide
Type "q"
Sleep 200ms
