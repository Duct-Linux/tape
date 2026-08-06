package logger

import (
	"log"
	"tape/common/global"
)

type Logger struct {
	Module    string
	Submodule string
}

func NewLogger(module string, submodule string) *Logger {
	return &Logger{
		Module:    module,
		Submodule: submodule,
	}
}

func (l *Logger) Log(message string) {
	if l.Module != "" {
		message = "[" + l.Module + "] " + "[" + l.Submodule + "] " + message
	}
	log.Println(message)
}

func (l *Logger) Info(message string) {
	l.Log("INFO: " + message)
}

func (l *Logger) VerboseInfo(message string) {
	if global.IsVerbose() {
		l.Info(message)
	}
}

func (l *Logger) Error(message string) {
	l.Log("ERROR: " + message)
}

func (l *Logger) VerboseError(message string) {
	if global.IsVerbose() {
		l.Error(message)
	}
}

func (l *Logger) Warning(message string) {
	l.Log("WARNING: " + message)
}

func (l *Logger) VerboseWarning(message string) {
	if global.IsVerbose() {
		l.Warning(message)
	}
}
