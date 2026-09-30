package web

import (
	"crypto/sha256"
	"fmt"
	"html/template"
)

// Local rebuilds commonly keep the release version. Fingerprint the embedded
// recovery assets so immutable browser caches cannot retain an older controller.
var mistralRecoveryAssetVersion = func() string {
	digest := sha256.New()
	for _, name := range []string{"mistral-recovery.js", "mistral-recovery.css"} {
		data, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			panic(err)
		}
		digest.Write(data)
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}()

var recoveryTemplateFuncs = template.FuncMap{
	"mistralRecoveryVersion": func() string { return mistralRecoveryAssetVersion },
}
