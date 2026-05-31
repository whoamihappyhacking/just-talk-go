package autotype

import (
	"log/slog"
)

// Paste inserts text into the currently focused input field.
func Paste(text string, logger *slog.Logger) error {
	return pastePlatform(text, logger)
}
