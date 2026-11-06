package verifyrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

func (d *driver) save() (problem error) {
	defer func() {
		if problem != nil {
			d.summary.Exit = 1
			for i := range d.summary.Features {
				d.summary.Features[i].Status = "failed"
				d.summary.Features[i].Reason += "; evidence write failed: " + problem.Error()
			}
			_ = d.saveText()
			_ = writeJSON(filepath.Join(d.evidence, "summary.json"), d.summary)
		}
	}()
	if err := d.saveText(); err != nil {
		return err
	}
	return writeJSON(filepath.Join(d.evidence, "summary.json"), d.summary)
}

func (d *driver) saveText() error {
	var text strings.Builder
	fmt.Fprintf(&text, "# Brain verification\n\nLevel %s. Exit %d.\n\n", d.summary.Level, d.summary.Exit)
	fmt.Fprintf(&text, "Executed binary SHA-256: %s.\nBuild source tree: %s.\n\n", d.summary.BinarySHA256, d.summary.SourceTree)
	for i, row := range d.summary.Features {
		fmt.Fprintf(&text, "%d. %s: %s. %s.\n", i+1, row.Name, row.Status, row.Reason)
	}
	if len(d.summary.StandIns) == 0 {
		text.WriteString("\nStand-ins: none.\n")
	} else {
		for _, standIn := range d.summary.StandIns {
			fmt.Fprintf(&text, "\nStand-in: %s\n", standIn)
		}
	}
	fmt.Fprintf(&text, "The documents are synthetic test inputs. Commands use the real binary.\n\nScratch removed: %t.\n\nCLI evidence uses command logs and file hashes. Screenshots do not apply.\n", d.summary.ScratchRemoved)
	problem := os.WriteFile(filepath.Join(d.evidence, "summary.md"), []byte(text.String()), 0600)
	page := "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><title>Brain verification</title><body><pre>" + html.EscapeString(text.String()) + "</pre><p><a href=\"summary.json\">Machine-readable results</a></p><ul>"
	for i := range d.summary.Commands {
		page += fmt.Sprintf("<li><a href=\"logs/%02d-command.json\">Command %d</a></li>", i+1, i+1)
	}
	page += "</ul></body></html>\n"
	return errors.Join(problem, os.WriteFile(filepath.Join(d.evidence, "index.html"), []byte(page), 0600))
}
