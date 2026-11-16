package secrets

import (
	_ "embed"
	"regexp"
	"strings"
	"unicode"
)

// The BIP-39 English list, byte for byte as published in bitcoin/bips
// (bip-0039/english.txt); wallet_test.go checks its sha256. The frontend keeps
// the same file for the same check (frontend/src/lib/secret-guard.ts).
//
//go:embed bip39_english.txt
var bip39English string

var bip39Words = func() map[string]struct{} {
	words := map[string]struct{}{}
	for _, w := range strings.Fields(bip39English) {
		words[w] = struct{}{}
	}
	return words
}()

var (
	// Explorer links carry transaction hashes, which have a private key's shape.
	explorerLink = regexp.MustCompile(`https?://[^ ]+`)
	// Exactly 32 bytes of hex, with or without 0x. Longer runs are signatures
	// or calldata.
	walletKey = regexp.MustCompile(`(?:^|[^0-9a-f])[0-9a-f]{64}(?:[^0-9a-f]|$)`)
	// "1." or "(1)" before a word, as wallets number the words they show.
	listNumber = regexp.MustCompile(`^\(?\d{1,2}[.):]?`)
)

// Quotes, brackets and sentence ends around a word. A colon stays on, so the
// keys of a pasted JSON object break a run of words.
const (
	opening = `"'“‘([{`
	closing = `"'”’)]}?!.;`
)

const phraseWords = 12

// HasWalletSecret reports whether text looks like it holds a private key or a
// BIP-39 recovery phrase, so a chat message carrying one is refused before it
// is stored or sent to a model. Shapes only, and the same rules the browser
// applies: a key glued to other hex, or a phrase with a typo, gets through.
func HasWalletSecret(text string) bool {
	// Space as JavaScript's \s or unicode.IsSpace sees it, and ASCII-only
	// lowercasing, so the browser and the server split text the same way.
	text = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r) || r == '\uFEFF':
			return ' '
		case 'A' <= r && r <= 'Z':
			return r + 'a' - 'A'
		}
		return r
	}, text)
	text = explorerLink.ReplaceAllString(text, " ")
	if walletKey.MatchString(text) {
		return true
	}
	run := 0
	for _, token := range strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == ',' }) {
		word := strings.TrimRight(strings.TrimLeft(listNumber.ReplaceAllString(token, ""), opening), closing)
		if word == "" {
			continue
		}
		if _, ok := bip39Words[word]; ok {
			run++
		} else {
			run = 0
		}
		if run >= phraseWords {
			return true
		}
	}
	return false
}
