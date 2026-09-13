# docs/social.tape — VHS script for the GitHub social preview image (1280x640).
#
# Records a short clip of the demo backend; the preview PNG is its last frame
# (see docs/README.md for the exact commands). Upload the PNG by hand:
# GitHub → Settings → General → Social preview.

Output docs/social.gif

Set Shell bash
Set FontSize 17
Set Width 1280
Set Height 640
Set Padding 24
Set Theme "TokyoNight"

Hide
Type "{ test -x ./d9c || go build -o d9c .; } && clear"
Enter
Sleep 4s
Type "./d9c -demo"
Enter
Sleep 3s
Down
# Force a redraw once the terminal has its final size (see demo.tape).
Type "r"
Sleep 2s
Show

Sleep 2s
