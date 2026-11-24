# Records docs/demo.gif from the demo data: vhs docs/demo.tape

Output docs/demo.gif

Set Shell bash
Set FontSize 15
Set Width 1600
Set Height 900
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 80ms

Hide
Type "go build -o /tmp/gerardo-demo . && export XDG_CONFIG_HOME=$(mktemp -d) && clear"
Enter
Sleep 3s
Type "/tmp/gerardo-demo -demo"
Enter
Sleep 1.5s
Show

# The dashboard, with a preview of the selected change.
Sleep 2.5s
Down 2
Sleep 1.5s
Up 2
Sleep 1s

# A change, its files, and the patch sets to compare.
Enter
Sleep 2.5s
Type "f"
Sleep 1.5s
Type "p"
Sleep 1.5s
Down
Sleep 1s
Enter
Sleep 1.5s

# The diff of PS1 against PS3, with the threads of PS1 on the left.
Down
Enter
Sleep 2s
Type "s"
Sleep 2.5s
Type "]"
Sleep 1.5s
Type "]"
Sleep 2.5s

Hide
Type "q"
Type "q"
Type "q"
Type "q"
