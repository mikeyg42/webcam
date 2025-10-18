package recorderlog

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Field is a structured logging field (zap-style).
type Field struct {
	Key   string
	Value any
}

// ---- Field helpers ----

func String(key, val string) Field   { return Field{Key: key, Value: val} }
func Bool(key string, val bool) Field { return Field{Key: key, Value: val} }
func Int(key string, val int) Field   { return Field{Key: key, Value: val} }
func Int64(key string, val int64) Field {
	return Field{Key: key, Value: val}
}
func Uint64(key string, val uint64) Field {
	return Field{Key: key, Value: val}
}
func Float64(key string, val float64) Field {
	return Field{Key: key, Value: val}
}
func Time(key string, v time.Time) Field {
	// store as value; formatter will render RFC3339Nano
	return Field{Key: key, Value: v}
}
func Duration(key string, d time.Duration) Field {
	// store as value; formatter will render d.String()
	return Field{Key: key, Value: d}
}
func Any(key string, val any) Field { return Field{Key: key, Value: val} }
func Error(err error) Field {
	if err == nil {
		return Field{Key: "error", Value: nil}
	}
	return Field{Key: "error", Value: err}
}

// Logger is the project-wide logging interface.
type Logger interface {
	// Named returns a child logger with the given component name appended.
	Named(name string) Logger
	// With returns a child logger that includes the provided fields.
	With(fields ...Field) Logger

	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
}

// ---- Global logger accessors ----

var (
	globalMu     sync.RWMutex
	globalLogger Logger = NewStdLogger()
)

// L returns the current global logger.
func L() Logger {
	globalMu.RLock()
	l := globalLogger
	globalMu.RUnlock()
	return l
}

// ReplaceGlobal swaps the global logger implementation.
func ReplaceGlobal(l Logger) {
	if l == nil {
		return
	}
	globalMu.Lock()
	globalLogger = l
	globalMu.Unlock()
}

// ---- stdlib-based logger implementation ----

type stdLogger struct {
	base   *log.Logger
	name   string
	fields []Field
	mu     sync.Mutex
}

// NewStdLogger creates a default logger that writes to stdout with time prefixes.
func NewStdLogger() Logger {
	return &stdLogger{
		base: log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds),
	}
}

// clone returns a shallow copy WITHOUT copying the internal mutex by value.
func (l *stdLogger) clone() *stdLogger {
	cp := &stdLogger{
		base: l.base,
		name: l.name,
	}
	if len(l.fields) > 0 {
		cp.fields = append([]Field(nil), l.fields...)
	}
	return cp
}

func (l *stdLogger) Named(name string) Logger {
	if name == "" {
		return l
	}
	cp := l.clone()
	if cp.name == "" {
		cp.name = name
	} else {
		cp.name = cp.name + "." + name
	}
	return cp
}

func (l *stdLogger) With(fields ...Field) Logger {
	if len(fields) == 0 {
		return l
	}
	cp := l.clone()
	cp.fields = append(cp.fields, fields...)
	return cp
}

func (l *stdLogger) Debug(msg string, fields ...Field) { l.log("DEBUG", msg, fields...) }
func (l *stdLogger) Info(msg string, fields ...Field)  { l.log("INFO", msg, fields...) }
func (l *stdLogger) Warn(msg string, fields ...Field)  { l.log("WARN", msg, fields...) }
func (l *stdLogger) Error(msg string, fields ...Field) { l.log("ERROR", msg, fields...) }

func (l *stdLogger) log(level, msg string, fields ...Field) {
	// lock to keep multi-field lines coherent
	l.mu.Lock()
	defer l.mu.Unlock()

	var b strings.Builder
	b.WriteString(level)
	if l.name != "" {
		b.WriteString(" name=")
		b.WriteString(quoteIfNeeded(l.name))
	}
	b.WriteString(" msg=")
	b.WriteString(quoteIfNeeded(msg))

	// merge static fields + call-specific fields
	all := make([]Field, 0, len(l.fields)+len(fields))
	all = append(all, l.fields...)
	all = append(all, fields...)

	if len(all) > 0 {
		// Sort keys for stable output
		sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
		for _, f := range all {
			if f.Key == "" {
				continue
			}
			b.WriteString(" ")
			b.WriteString(f.Key)
			b.WriteByte('=')
			b.WriteString(quoteIfNeeded(fmtVal(f.Value)))
		}
	}

	l.base.Println(b.String())
}

func fmtVal(v any) string {
	if v == nil {
		return "null"
	}
	switch t := v.(type) {
	case error:
		return t.Error()
	case string:
		return t
	case time.Time:
		// Use a stable, parseable representation
		return t.UTC().Format(time.RFC3339Nano)
	case time.Duration:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}

func quoteIfNeeded(s string) string {
	// Quote if whitespace or equals present for safer parsing
	if strings.ContainsAny(s, " \t\n\r=") {
		return fmt.Sprintf("%q", s)
	}
	return s
}
