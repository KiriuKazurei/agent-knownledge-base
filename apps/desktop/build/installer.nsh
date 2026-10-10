!macro customInstall
  ; Write only during an actual NSIS installation. Never include this marker
  ; via extraResources / afterPack: NSIS and portable share the app archive.
  FileOpen $0 "$INSTDIR\resources\kah-installation.json" w
  FileWrite $0 '{"mode":"installed"}'
  FileClose $0
!macroend
