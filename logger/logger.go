package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"sync"
	"time"
)

// LogLevel represents the severity of a log entry
type LogLevel int

const (
	// DEBUG level for debug information
	DEBUG LogLevel = iota
	// INFO level for informational messages
	INFO
	// WARN level for warning messages
	WARN
	// ERROR level for error messages
	ERROR
	// FATAL level for fatal errors
	FATAL
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp   time.Time              `json:"timestamp"`
	Level       string                 `json:"level"`
	Message     string                 `json:"message"`
	Fields      map[string]interface{} `json:"fields,omitempty"`
	Caller      string                 `json:"caller,omitempty"`
	AgentName   string                 `json:"agent,omitempty"`
	RequestID   string                 `json:"request_id,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

// Logger provides structured logging capabilities
type Logger struct {
	mu          sync.RWMutex
	level       LogLevel
	output      io.Writer
	fields      map[string]interface{}
	agentName   string
	jsonFormat  bool
	includeCaller bool
}

// Global logger instance
var (
	globalLogger *Logger
	once         sync.Once
)

// init initializes the global logger
func init() {
	globalLogger = New()
}

// New creates a new logger instance
func New() *Logger {
	return &Logger{
		level:         INFO,
		output:        os.Stdout,
		fields:        make(map[string]interface{}),
		jsonFormat:    true,
		includeCaller: true,
	}
}

// GetLogger returns the global logger instance
func GetLogger() *Logger {
	once.Do(func() {
		if globalLogger == nil {
			globalLogger = New()
		}
	})
	return globalLogger
}

// SetLevel sets the minimum log level
func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// SetOutput sets the output writer
func (l *Logger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output = w
}

// SetJSONFormat enables or disables JSON formatting
func (l *Logger) SetJSONFormat(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jsonFormat = enabled
}

// SetAgentName sets the agent name for all log entries
func (l *Logger) SetAgentName(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.agentName = name
}

// SetIncludeCaller enables or disables caller information
func (l *Logger) SetIncludeCaller(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.includeCaller = enabled
}

// WithField creates a new logger with an additional field
func (l *Logger) WithField(key string, value interface{}) *Logger {
	l.mu.RLock()
	defer l.mu.RUnlock()

	newLogger := &Logger{
		level:         l.level,
		output:        l.output,
		fields:        make(map[string]interface{}),
		agentName:     l.agentName,
		jsonFormat:    l.jsonFormat,
		includeCaller: l.includeCaller,
	}

	// Copy existing fields
	for k, v := range l.fields {
		newLogger.fields[k] = v
	}
	newLogger.fields[key] = value

	return newLogger
}

// WithFields creates a new logger with additional fields
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	l.mu.RLock()
	defer l.mu.RUnlock()

	newLogger := &Logger{
		level:         l.level,
		output:        l.output,
		fields:        make(map[string]interface{}),
		agentName:     l.agentName,
		jsonFormat:    l.jsonFormat,
		includeCaller: l.includeCaller,
	}

	// Copy existing fields
	for k, v := range l.fields {
		newLogger.fields[k] = v
	}
	// Add new fields
	for k, v := range fields {
		newLogger.fields[k] = v
	}

	return newLogger
}

// log writes a log entry
func (l *Logger) log(level LogLevel, msg string, err error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if level < l.level {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level.String(),
		Message:   msg,
		Fields:    l.fields,
		AgentName: l.agentName,
	}

	if err != nil {
		entry.Error = err.Error()
	}

	if l.includeCaller {
		if _, file, line, ok := runtime.Caller(2); ok {
			entry.Caller = fmt.Sprintf("%s:%d", file, line)
		}
	}

	// Add request ID if present in fields
	if reqID, ok := l.fields["request_id"]; ok {
		entry.RequestID = fmt.Sprintf("%v", reqID)
	}

	if l.jsonFormat {
		l.writeJSON(entry)
	} else {
		l.writeText(entry)
	}

	// Exit on fatal
	if level == FATAL {
		os.Exit(1)
	}
}

// writeJSON writes a log entry as JSON
func (l *Logger) writeJSON(entry LogEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("Failed to marshal log entry: %v", err)
		return
	}
	fmt.Fprintln(l.output, string(data))
}

// writeText writes a log entry as plain text
func (l *Logger) writeText(entry LogEntry) {
	var output string

	// Format timestamp
	output = fmt.Sprintf("[%s] ", entry.Timestamp.Format("2006-01-02 15:04:05"))

	// Add level
	output += fmt.Sprintf("[%s] ", entry.Level)

	// Add agent name if present
	if entry.AgentName != "" {
		output += fmt.Sprintf("[%s] ", entry.AgentName)
	}

	// Add request ID if present
	if entry.RequestID != "" {
		output += fmt.Sprintf("[%s] ", entry.RequestID)
	}

	// Add message
	output += entry.Message

	// Add error if present
	if entry.Error != "" {
		output += fmt.Sprintf(" error=%s", entry.Error)
	}

	// Add fields
	for k, v := range entry.Fields {
		if k != "request_id" { // Skip request_id as it's already displayed
			output += fmt.Sprintf(" %s=%v", k, v)
		}
	}

	// Add caller if present
	if entry.Caller != "" {
		output += fmt.Sprintf(" caller=%s", entry.Caller)
	}

	fmt.Fprintln(l.output, output)
}

// Debug logs a debug message
func (l *Logger) Debug(msg string) {
	l.log(DEBUG, msg, nil)
}

// Debugf logs a formatted debug message
func (l *Logger) Debugf(format string, args ...interface{}) {
	l.log(DEBUG, fmt.Sprintf(format, args...), nil)
}

// Info logs an info message
func (l *Logger) Info(msg string) {
	l.log(INFO, msg, nil)
}

// Infof logs a formatted info message
func (l *Logger) Infof(format string, args ...interface{}) {
	l.log(INFO, fmt.Sprintf(format, args...), nil)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string) {
	l.log(WARN, msg, nil)
}

// Warnf logs a formatted warning message
func (l *Logger) Warnf(format string, args ...interface{}) {
	l.log(WARN, fmt.Sprintf(format, args...), nil)
}

// Error logs an error message
func (l *Logger) Error(msg string, err error) {
	l.log(ERROR, msg, err)
}

// Errorf logs a formatted error message
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.log(ERROR, fmt.Sprintf(format, args...), nil)
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(msg string, err error) {
	l.log(FATAL, msg, err)
}

// Fatalf logs a formatted fatal message and exits
func (l *Logger) Fatalf(format string, args ...interface{}) {
	l.log(FATAL, fmt.Sprintf(format, args...), nil)
}

// Global logging functions

// Debug logs a debug message using the global logger
func Debug(msg string) {
	GetLogger().Debug(msg)
}

// Debugf logs a formatted debug message using the global logger
func Debugf(format string, args ...interface{}) {
	GetLogger().Debugf(format, args...)
}

// Info logs an info message using the global logger
func Info(msg string) {
	GetLogger().Info(msg)
}

// Infof logs a formatted info message using the global logger
func Infof(format string, args ...interface{}) {
	GetLogger().Infof(format, args...)
}

// Warn logs a warning message using the global logger
func Warn(msg string) {
	GetLogger().Warn(msg)
}

// Warnf logs a formatted warning message using the global logger
func Warnf(format string, args ...interface{}) {
	GetLogger().Warnf(format, args...)
}

// Error logs an error message using the global logger
func Error(msg string, err error) {
	GetLogger().Error(msg, err)
}

// Errorf logs a formatted error message using the global logger
func Errorf(format string, args ...interface{}) {
	GetLogger().Errorf(format, args...)
}

// Fatal logs a fatal message using the global logger and exits
func Fatal(msg string, err error) {
	GetLogger().Fatal(msg, err)
}

// Fatalf logs a formatted fatal message using the global logger and exits
func Fatalf(format string, args ...interface{}) {
	GetLogger().Fatalf(format, args...)
}

// SetGlobalLevel sets the global logger level
func SetGlobalLevel(level LogLevel) {
	GetLogger().SetLevel(level)
}

// SetGlobalOutput sets the global logger output
func SetGlobalOutput(w io.Writer) {
	GetLogger().SetOutput(w)
}

// SetGlobalAgentName sets the global logger agent name
func SetGlobalAgentName(name string) {
	GetLogger().SetAgentName(name)
}

// ParseLevel parses a string log level
func ParseLevel(levelStr string) (LogLevel, error) {
	switch levelStr {
	case "DEBUG", "debug":
		return DEBUG, nil
	case "INFO", "info":
		return INFO, nil
	case "WARN", "warn", "WARNING", "warning":
		return WARN, nil
	case "ERROR", "error":
		return ERROR, nil
	case "FATAL", "fatal":
		return FATAL, nil
	default:
		return INFO, fmt.Errorf("unknown log level: %s", levelStr)
	}
}