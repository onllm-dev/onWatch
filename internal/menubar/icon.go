package menubar

import _ "embed"

//go:embed icon_template.png
var IconTemplate []byte

//go:embed icon_template@2x.png
var IconTemplate2x []byte

//go:embed icon_template.pdf
var IconPDF []byte

func trayIcons() ([]byte, []byte) {
	return IconTemplate, IconTemplate2x
}

func trayIconPDF() []byte {
	return IconPDF
}
