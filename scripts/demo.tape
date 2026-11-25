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
Set Theme { "name": "aiswap", "background": "#0E1412", "foreground": "#E6EEE9", "cursor": "#55E0A3", "selection": "#14261D", "black": "#0E1412", "red": "#F87171", "green": "#55E0A3", "yellow": "#FBBF24", "blue": "#60A5FA", "magenta": "#C084FC", "cyan": "#5EEAD4", "white": "#E6EEE9", "brightBlack": "#6B7280", "brightRed": "#FCA5A5", "brightGreen": "#CEE432", "brightYellow": "#FDE68A", "brightBlue": "#93C5FD", "brightMagenta": "#D8B4FE", "brightCyan": "#99F6E4", "brightWhite": "#FFFFFF" }
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
