# Records assets/mping-tui.gif for the README.
# Run from the repository root with an installed (privileged) mping:
#   vhs assets/demo/tui.tape
# If the Wait below times out, the TUI did not come up in VHS's terminal;
# run the tape again.

Output assets/mping-tui.gif

Require mping

Set Shell "bash"
Set FontSize 14
Set Width 1440
Set Height 900
Set Padding 16
Set Framerate 8

# Let the statistics and RTT graphs fill up before recording.
Hide
Type "mping -f assets/demo/hosts.yaml"
Enter
Wait+Screen@15s /Ping Monitor/
Sleep 20s
Show

Sleep 10s

Hide
Type "q"
Sleep 2s
