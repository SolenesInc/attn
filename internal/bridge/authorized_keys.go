package bridge

import (
	"bytes"
	"fmt"

	"golang.org/x/crypto/ssh"
)

func AuthorizedKeys(data []byte, logf func(string, ...any)) []ssh.PublicKey {
	var keys []ssh.PublicKey
	for i, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		key, _, options, _, err := ssh.ParseAuthorizedKey(line)
		if err != nil {
			if logf != nil {
				logf("authorized_keys line %d: %v", i+1, err)
			}
			continue
		}
		if len(options) != 0 {
			if logf != nil {
				logf("authorized_keys line %d: skipped key with options %v", i+1, options)
			}
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

func authorize(data []byte, candidate ssh.PublicKey, logf func(string, ...any)) error {
	for _, key := range AuthorizedKeys(data, logf) {
		if bytes.Equal(key.Marshal(), candidate.Marshal()) {
			return nil
		}
	}
	return fmt.Errorf("key %s is not accepted by authorized_keys", ssh.FingerprintSHA256(candidate))
}
