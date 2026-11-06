package query

import (
	"regexp"
	"strings"
	"unicode"
)

type Plan struct {
	First, Fallback string
	Mode            string
	Boost           []string
}

var tokens = regexp.MustCompile(`"([^"]*)"|([^\s\p{Z}\x{0085}\x{000b}]+)`)

func operator(text string) bool { return text == "AND" || text == "OR" || text == "NOT" }
func plain(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			return false
		}
	}
	return true
}
func Make(typed string, projects []string) *Plan {
	names := map[string]string{}
	for _, name := range projects {
		names[strings.ToLower(name)] = name
	}
	terms, boost := []string{}, []string{}
	for _, token := range tokens.FindAllStringSubmatch(typed, -1) {
		phrase, word := token[1], token[2]
		if operator(word) {
			if len(terms) != 0 && !operator(terms[len(terms)-1]) {
				terms = append(terms, word)
			}
			continue
		}
		text := phrase
		if text == "" {
			text = word
		}
		text = strings.ReplaceAll(text, `"`, "")
		hasWord := false
		for _, r := range text {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				hasWord = true
				break
			}
		}
		if !hasWord {
			continue
		}
		trimmed := strings.Trim(text, "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~ ")
		if name := names[strings.ToLower(trimmed)]; name != "" {
			found := false
			for _, previous := range boost {
				if previous == name {
					found = true
				}
			}
			if !found {
				boost = append(boost, name)
			}
		}
		if word == "" || !plain(text) {
			text = `"` + text + `"`
		}
		terms = append(terms, text)
	}
	for len(terms) != 0 && operator(terms[len(terms)-1]) {
		terms = terms[:len(terms)-1]
	}
	if len(terms) == 0 {
		return nil
	}
	plan := &Plan{First: strings.Join(terms, " "), Boost: boost, Mode: mode(terms)}
	words := []string{}
	for _, term := range terms {
		if !operator(term) {
			words = append(words, term)
		}
	}
	if len(words) > 1 && len(words) == len(terms) {
		plan.Fallback = strings.Join(words, " OR ")
	}
	return plan
}

func mode(terms []string) string {
	and, or, not := false, false, false
	for i, term := range terms {
		switch term {
		case "AND":
			and = true
		case "OR":
			or = true
		case "NOT":
			not = true
		default:
			if i > 0 && !operator(terms[i-1]) {
				and = true
			}
		}
	}
	if not || and && or {
		return "expression"
	}
	if or {
		return "or"
	}
	return "and"
}
