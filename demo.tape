Output demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 700
Set Theme "Dracula"
Set Padding 20
Set Framerate 30
Set PlaybackSpeed 1

# Set up working directory silently via env var approach
Hide
Type "cd /Users/mmahut/code/oc-find-waste && export PATH=$PWD:$PATH && clear"
Enter
Sleep 800ms
Show

# Show flags available
Type "oc-find-waste scan --help"
Sleep 400ms
Enter
Sleep 3500ms

# Main demo: scan with AWS pricing
Ctrl+L
Sleep 400ms

Type "oc-find-waste scan -n mmahut-iohk-dev --pricing aws"
Sleep 400ms
Enter
Sleep 7s

# JSON output
Ctrl+L
Sleep 400ms

Type "oc-find-waste scan -n mmahut-iohk-dev --pricing aws -o json"
Sleep 400ms
Enter
Sleep 5500ms

Sleep 2s
