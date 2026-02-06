// Package internal ensures dependencies are tracked in go.mod
// This file will be removed once actual implementation files are created
package internal

import (
	_ "github.com/joho/godotenv"
	_ "modernc.org/sqlite"
)
