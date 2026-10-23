package store

import (
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

// Sealed values are "e1:" + base64(nonce || XChaCha20-Poly1305 ciphertext). The associated data
// binds each value to its row and column, so ciphertexts can't be swapped between messages.
// Empty strings stay empty: queries rely on name != ”.
const sealedPrefix = "e1:"

var ErrWrongKey = errors.New("ttyl.db was encrypted with a different key (the \"ttyl\" Keychain item is missing or was replaced); move ttyl.db away to start fresh")

type sealer struct{ aead cipher.AEAD } // nil aead: no key, values stay plaintext

func newSealer(key []byte) (sealer, error) {
	if key == nil {
		return sealer{}, nil
	}
	aead, err := chacha20poly1305.NewX(key)
	return sealer{aead}, err
}

func (s sealer) seal(plain string, aad ...string) string {
	if s.aead == nil || plain == "" {
		return plain
	}
	nonce := make([]byte, s.aead.NonceSize(), s.aead.NonceSize()+len(plain)+s.aead.Overhead())
	rand.Read(nonce)
	return sealedPrefix + base64.RawStdEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(plain), joinAAD(aad)))
}

// open decrypts a sealed value; anything without the prefix is legacy plaintext and returned as is.
func (s sealer) open(v string, aad ...string) (string, error) {
	if !strings.HasPrefix(v, sealedPrefix) {
		return v, nil
	}
	if s.aead == nil {
		return "", errors.New("the message store is encrypted but no key was given")
	}
	raw, err := base64.RawStdEncoding.DecodeString(v[len(sealedPrefix):])
	if err != nil || len(raw) < s.aead.NonceSize() {
		return "", errors.New("corrupt sealed value")
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], joinAAD(aad))
	if err != nil {
		return "", ErrWrongKey
	}
	return string(plain), nil
}

func (s sealer) sealBytes(b []byte, aad ...string) []byte {
	if b == nil {
		return nil
	}
	return []byte(s.seal(string(b), aad...))
}

func (s sealer) openBytes(b []byte, aad ...string) ([]byte, error) {
	if b == nil {
		return nil, nil
	}
	v, err := s.open(string(b), aad...)
	return []byte(v), err
}

func joinAAD(parts []string) []byte { return []byte(strings.Join(parts, "\x00")) }

func msgAAD(platform, chatID, id, column string) []string {
	return []string{"message", platform, chatID, id, column}
}

func chatAAD(platform, id string) []string { return []string{"chat", platform, id, "name"} }

// initKey checks the key against the database and seals rows written before encryption existed.
func (s *Store) initKey() error {
	check, err := s.setting("keycheck")
	if err != nil {
		return err
	}
	if s.sealer.aead == nil {
		if check != "" {
			return errors.New("the message store is encrypted but no key was given")
		}
		return nil
	}
	if check != "" {
		if _, err := s.sealer.open(check, "keycheck"); err != nil {
			return ErrWrongKey
		}
	} else if err := s.putSetting("keycheck", s.sealer.seal("dmz", "keycheck")); err != nil { // only decryptability matters; value predates the ttyl rename
		return err
	}
	if done, err := s.setting("sealed"); err != nil || done == "1" {
		return err
	}
	if err := s.sealPlaintext(); err != nil {
		return fmt.Errorf("encrypt existing messages: %w", err)
	}
	return nil
}

func (s *Store) sealPlaintext() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	type chatRow struct{ platform, id, name string }
	var chats []chatRow
	// Nothing is sealed before this runs once, so every value is plaintext, even one that
	// happens to start with "e1:". The "sealed" flag commits in the same transaction.
	rows, err := tx.Query(`SELECT platform, id, name FROM chats WHERE name != ''`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r chatRow
		if err := rows.Scan(&r.platform, &r.id, &r.name); err != nil {
			rows.Close()
			return err
		}
		chats = append(chats, r)
	}
	rows.Close()
	for _, r := range chats {
		if _, err := tx.Exec(`UPDATE chats SET name = ? WHERE platform = ? AND id = ?`,
			s.sealer.seal(r.name, chatAAD(r.platform, r.id)...), r.platform, r.id); err != nil {
			return err
		}
	}

	type msgRow struct {
		platform, chatID, id, sender, body, meta string
		media                                    []byte
	}
	var msgs []msgRow
	rows, err = tx.Query(`SELECT platform, chat_id, id, sender_name, body, meta, media FROM messages`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r msgRow
		if err := rows.Scan(&r.platform, &r.chatID, &r.id, &r.sender, &r.body, &r.meta, &r.media); err != nil {
			rows.Close()
			return err
		}
		msgs = append(msgs, r)
	}
	rows.Close()
	for _, r := range msgs {
		aad := func(col string) []string { return msgAAD(r.platform, r.chatID, r.id, col) }
		if _, err := tx.Exec(`UPDATE messages SET sender_name = ?, body = ?, meta = ?, media = ? WHERE platform = ? AND chat_id = ? AND id = ?`,
			s.sealer.seal(r.sender, aad("sender_name")...), s.sealer.seal(r.body, aad("body")...), s.sealer.seal(r.meta, aad("meta")...),
			s.sealer.sealBytes(r.media, aad("media")...), r.platform, r.chatID, r.id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES ('sealed', '1') ON CONFLICT (key) DO UPDATE SET value = '1'`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) setting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) putSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
