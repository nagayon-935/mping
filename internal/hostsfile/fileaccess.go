package hostsfile

import (
	"os"

	"github.com/nagayon-935/mping/internal/privilege"
)

// asRealUser runs every hosts-file and include-file access as the invoking
// user. A setuid-root mping would otherwise read any path a hosts file
// names with root's permissions; with it, the kernel applies the invoking
// user's permissions instead. Tests swap it to verify that every file
// access goes through it.
var asRealUser = privilege.AsRealUser

// readFileAsRealUser reads path as the invoking user. Errors are returned
// unwrapped so each caller keeps its own message.
func readFileAsRealUser(path string) ([]byte, error) {
	var data []byte
	err := asRealUser(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}
