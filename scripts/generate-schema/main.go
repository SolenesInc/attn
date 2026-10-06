package main

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/victorarias/attn/internal/store"
)

func main() {
	if err := dump(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func dump() error {
	db, err := store.OpenDB(":memory:")
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY type, name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(os.Stdout, normalizeSQL(statement)+";"); err != nil {
			return err
		}
	}
	return rows.Err()
}

func normalizeSQL(statement string) string {
	var out strings.Builder
	var quote rune
	space := false
	chars := []rune(strings.TrimSpace(statement))
	for i := 0; i < len(chars); i++ {
		char := chars[i]
		if quote != 0 {
			out.WriteRune(char)
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '-' && i+1 < len(chars) && chars[i+1] == '-' {
			for i < len(chars) && chars[i] != '\n' {
				i++
			}
			space = true
			continue
		}
		if char == '/' && i+1 < len(chars) && chars[i+1] == '*' {
			i += 2
			for i+1 < len(chars) && !(chars[i] == '*' && chars[i+1] == '/') {
				i++
			}
			i++
			space = true
			continue
		}
		if unicode.IsSpace(char) {
			space = true
			continue
		}
		if space && out.Len() != 0 {
			out.WriteByte(' ')
		}
		space = false
		switch char {
		case '\'', '"', '`':
			quote = char
		case '[':
			quote = ']'
		}
		out.WriteRune(char)
	}
	return strings.TrimSuffix(out.String(), ";")
}
