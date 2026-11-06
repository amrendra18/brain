//go:build !darwin

package platform

import "os"

func privateACL(*os.File) error { return nil }
