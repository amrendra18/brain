package platform

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

func Command(shell string, argv []string) string {
	if shell == "cmd" {
		// Encoded text avoids cmd's percent expansion and delayed bang expansion.
		units := utf16.Encode([]rune(nativePowerShell(argv)))
		data := make([]byte, 2*len(units))
		for i, unit := range units {
			binary.LittleEndian.PutUint16(data[2*i:], unit)
		}
		return `"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -NonInteractive -EncodedCommand ` + base64.StdEncoding.EncodeToString(data)
	}
	if shell == "powershell" {
		for _, word := range argv {
			if strings.Contains(word, `"`) {
				return nativePowerShell(argv)
			}
		}
	}
	parts := make([]string, len(argv))
	for i, word := range argv {
		switch shell {
		case "powershell":
			parts[i] = psLiteral(word)
		default:
			parts[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(word) + `"`
		}
	}
	prefix := ""
	if shell == "powershell" {
		prefix = "& "
	}
	return prefix + strings.Join(parts, " ")
}

func psLiteral(word string) string {
	return "'" + strings.NewReplacer("'", "''", "‘", "‘‘", "’", "’’").Replace(word) + "'"
}

// Windows PowerShell 5 strips native argument quotes. ProcessStartInfo avoids that parser.
func nativePowerShell(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	parts := make([]string, len(argv)-1)
	for i, word := range argv[1:] {
		parts[i] = windowsArg(word)
	}
	return "$p = New-Object System.Diagnostics.ProcessStartInfo; $p.FileName = " + psLiteral(argv[0]) + "; $p.UseShellExecute = $false; $p.Arguments = " + psLiteral(strings.Join(parts, " ")) + "; $c = [System.Diagnostics.Process]::Start($p); $c.WaitForExit(); exit $c.ExitCode"
}

// Windows native argv doubles backslashes before quotes and the closing quote.
func windowsArg(word string) string {
	var result strings.Builder
	result.WriteByte('"')
	slashes := 0
	for _, character := range word {
		if character == '\\' {
			slashes++
			continue
		}
		if character == '"' {
			result.WriteString(strings.Repeat(`\`, 2*slashes+1))
		} else {
			result.WriteString(strings.Repeat(`\`, slashes))
		}
		slashes = 0
		result.WriteRune(character)
	}
	result.WriteString(strings.Repeat(`\`, 2*slashes))
	result.WriteByte('"')
	return result.String()
}
