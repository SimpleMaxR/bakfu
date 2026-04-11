package merge

import (
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

// Chromium LocalStorage leveldb key format: "_" + origin + "\x00\x01" + key
var chromiumPersistKey = []byte("_file://\x00\x01persist:cherry-studio")
var simplePersistKey = []byte("persist:cherry-studio")

// ReadPersistCherryStudio reads persist:cherry-studio from leveldb
func ReadPersistCherryStudio(leveldbDir string) (string, error) {
	db, err := leveldb.OpenFile(leveldbDir, &opt.Options{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer db.Close()

	// Try Chromium format key first
	value, err := db.Get(chromiumPersistKey, nil)
	if err == nil {
		return decodeLevelDBValue(value), nil
	}

	// Fall back to simple key (used in tests and older bakfu output)
	value, err = db.Get(simplePersistKey, nil)
	if err == leveldb.ErrNotFound {
		return "{}", nil
	}
	if err != nil {
		return "", err
	}

	return string(value), nil
}

// decodeLevelDBValue decodes a Chromium LocalStorage leveldb value.
// Chromium stores values with a 1-byte prefix: 0x00 for UTF-16LE, 0x01 for Latin-1.
func decodeLevelDBValue(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}

	encoding := raw[0]
	payload := raw[1:]

	if encoding == 0x00 {
		// UTF-16LE encoded
		return DecodeUTF16LE(payload)
	}
	// Latin-1 (0x01) or unknown: treat as raw bytes
	return string(payload)
}

// DecodeUTF16LE decodes UTF-16LE bytes to string
func DecodeUTF16LE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	runes := make([]rune, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		r := rune(b[i]) | rune(b[i+1])<<8
		runes = append(runes, r)
	}
	return string(runes)
}

// EncodeUTF16LE encodes string to UTF-16LE bytes
func EncodeUTF16LE(s string) []byte {
	runes := []rune(s)
	buf := make([]byte, len(runes)*2)
	for i, r := range runes {
		buf[i*2] = byte(r & 0xFF)
		buf[i*2+1] = byte((r >> 8) & 0xFF)
	}
	return buf
}

// WritePersistCherryStudio writes persist:cherry-studio to leveldb
func WritePersistCherryStudio(leveldbDir, value string) error {
	db, err := leveldb.OpenFile(leveldbDir, nil)
	if err != nil {
		return err
	}
	defer db.Close()

	// Check if chromium key exists; if so, write in chromium format
	_, err = db.Get(chromiumPersistKey, nil)
	if err == nil {
		// Encode as UTF-16LE with 0x00 prefix (Chromium format)
		encoded := append([]byte{0x00}, EncodeUTF16LE(value)...)
		return db.Put(chromiumPersistKey, encoded, nil)
	}

	// No chromium key exists — write with simple key
	return db.Put(simplePersistKey, []byte(value), nil)
}

// GetChromiumPersistKey returns the Chromium persist key (for tests)
func GetChromiumPersistKey() []byte {
	return chromiumPersistKey
}
