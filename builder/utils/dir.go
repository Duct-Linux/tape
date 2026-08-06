package utils

import "path"

func DirWork(pkgPath string) string {
	return path.Join(pkgPath, "work")
}

func DirWorkInstall(pkgPath string) string {
	return path.Join(pkgPath, "work/install")
}

func DirWrap(pkgPath string) string {
	return path.Join(pkgPath, "wrap")
}

func DirWrapInstall(pkgPath string) string {
	return path.Join(pkgPath, "wrap/install")
}
