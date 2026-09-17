package repobot

import "os"

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0600) }
