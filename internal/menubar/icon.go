package menubar

import _ "embed"

//go:embed icon_template.png
var IconTemplate []byte

//go:embed icon_template@2x.png
var IconTemplate2x []byte

func trayIcons() ([]byte, []byte) {
	return IconTemplate, IconTemplate2x
}
