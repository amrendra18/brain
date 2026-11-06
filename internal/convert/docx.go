package convert

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
)

const maxDocxText = 16 * 1024 * 1024

// Docx reads the main document, keeping headings, table rows and link text.
func Docx(data []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var document *zip.File
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		return "", errors.New("docx has no main document")
	}
	if document.UncompressedSize64 > maxDocxText {
		return "", errors.New("docx text exceeds 16 MB")
	}
	stream, err := document.Open()
	if err != nil {
		return "", err
	}
	defer stream.Close()
	text, err := io.ReadAll(io.LimitReader(stream, maxDocxText+1))
	if err != nil {
		return "", err
	}
	if len(text) > maxDocxText {
		return "", errors.New("docx text exceeds 16 MB")
	}
	return documentText(text)
}

func documentText(data []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	paragraph := docxText{}
	style := ""
	reading, deleted := false, 0
	blocks := []string{"body"}
	lines := docxText{separator: "\n\n"}
	cells := docxText{separator: " | "}
	paragraphs := docxText{separator: " "}
	destination := &lines
	styles := []string{}
	flush := func() error {
		text := strings.TrimSpace(paragraph.text.String())
		paragraph = docxText{}
		if text == "" {
			return nil
		}
		if strings.HasPrefix(style, "Heading") {
			level, err := strconv.Atoi(strings.TrimPrefix(style, "Heading"))
			if err == nil && level >= 1 && level <= 6 {
				text = strings.Repeat("#", level) + " " + text
			}
		}
		if style == "ListBullet" {
			text = "- " + text
		}
		return destination.add(text)
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch element := token.(type) {
		case xml.StartElement:
			name := element.Name.Local
			parent := blocks[len(blocks)-1]
			switch name {
			case "p", "tbl", "tr", "tc", "txbxContent":
				valid := name == "p" && (parent == "body" || parent == "tc" || parent == "txbxContent") ||
					name == "tbl" && (parent == "body" || parent == "txbxContent" && destination == &lines) ||
					name == "tr" && parent == "tbl" || name == "tc" && parent == "tr" ||
					name == "txbxContent" && parent == "p"
				if !valid {
					return "", errors.New("nested or misplaced docx blocks are unsupported")
				}
				blocks = append(blocks, name)
			}
			switch name {
			case "del":
				deleted++
			case "tr":
				cells = docxText{separator: " | "}
			case "tc":
				paragraphs = docxText{separator: " "}
				destination = &paragraphs
			case "txbxContent":
				if err := flush(); err != nil {
					return "", err
				}
				styles = append(styles, style)
			case "p":
				paragraph = docxText{}
				style = ""
			case "pStyle":
				for _, attribute := range element.Attr {
					if attribute.Name.Local == "val" {
						style = attribute.Value
					}
				}
			case "t":
				reading = true
			case "tab":
				if deleted == 0 {
					if err := paragraph.add("\t"); err != nil {
						return "", err
					}
				}
			case "br":
				if deleted == 0 {
					if err := paragraph.add("\n"); err != nil {
						return "", err
					}
				}
			}
		case xml.CharData:
			if reading && deleted == 0 {
				if err := paragraph.add(string(element)); err != nil {
					return "", err
				}
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "p", "tbl", "tr", "tc", "txbxContent":
				blocks = blocks[:len(blocks)-1]
			}
			switch element.Name.Local {
			case "del":
				deleted--
			case "t":
				reading = false
			case "tc":
				if err := cells.add(paragraphs.text.String()); err != nil {
					return "", err
				}
				destination = &lines
			case "txbxContent":
				style = styles[len(styles)-1]
				styles = styles[:len(styles)-1]
			case "tr":
				if cells.parts > 0 {
					if err := lines.add(cells.text.String()); err != nil {
						return "", err
					}
				}
			case "p":
				if err := flush(); err != nil {
					return "", err
				}
			}
		}
	}
	return lines.text.String(), nil
}

type docxText struct {
	text      strings.Builder
	separator string
	parts     int
}

func (group *docxText) add(text string) error {
	separator := ""
	if group.parts > 0 {
		separator = group.separator
	}
	if len(text)+len(separator) > maxDocxText-group.text.Len() {
		return errors.New("docx text exceeds 16 MB")
	}
	group.text.WriteString(separator)
	group.text.WriteString(text)
	group.parts++
	return nil
}
