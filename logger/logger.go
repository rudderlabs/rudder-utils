/*
Logger Interface Use instance of logger instead of exported functions

usage example

import (
	"errors"
	"github.com/rudderlabs/rudder-utils/logger"
)

var	log logger.LoggerI  = &logger.LoggerT{}
			or
var	log logger.LoggerI = logger.NewLogger()

...

log.Error(...)
*/
//go:generate mockgen -destination=../../mocks/utils/logger/mock_logger.go -package mock_logger github.com/rudderlabs/rudder-server/utils/logger LoggerI
package logger

import (
	"bytes"
	"errors"
	"go.uber.org/zap"
	"io/ioutil"
	"net/http"
	"runtime"
	"strings"
	"sync"
)

/*
Using levels(like Debug, Info etc.) in logging is a way to categorize logs based on their importance.
The idea is to have the option of running the application in different logging levels based on
how verbose we want the logging to be.
For example, using Debug level of logging, logs everything and it might slow the application, so we run application
in DEBUG level for local development or when we want to look through the entire flow of events in detail.
We use 4 logging levels here Debug, Info, Error and Fatal.
*/

type LoggerI interface {
	IsDebugLevel() bool
	Debug(args ...interface{})
	Info(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})
	Fatal(args ...interface{})
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
	LogRequest(req *http.Request)
	Child(s string) LoggerI
}

type LoggerT struct {
	name   string
	parent *LoggerT
	config ConfigLogger
}

type ConfigLogger struct {
	RootLevel           string
	EnableConsole       bool
	EnableFile          bool
	ConsoleJsonFormat   bool
	FileJsonFormat      bool
	LogFileLocation     string
	LogFileSize         int
	EnableTimestamp     bool
	EnableFileNameInLog bool
	EnableStackTrace    bool
	LevelConfigStr      string
}

const (
	levelEvent = iota // Logs Event
	levelDebug        // Most verbose logging level
	levelInfo         // Logs about state of the application
	levelWarn         // Logs about warnings
	levelError        // Logs about errors which dont immediately halt the application
	levelFatal        // Logs which crashes the application
)

var levelMap = map[string]int{
	"EVENT": levelEvent,
	"DEBUG": levelDebug,
	"INFO":  levelInfo,
	"WARN":  levelWarn,
	"ERROR": levelError,
	"FATAL": levelFatal,
}

var (
	enableConsole       bool
	enableFile          bool
	consoleJsonFormat   bool
	fileJsonFormat      bool
	rootLevel           int
	enableTimestamp     bool
	enableFileNameInLog bool
	enableStackTrace    bool
	logFileLocation     string
	logFileSize         int
	DefaultConfigLogger ConfigLogger
)

var (
	Log *zap.SugaredLogger
	//	log               = NewLogger()    Need to check where is it being used???????Somewhere in the previous package ?
	levelConfig       map[string]int
	loggerLevelsCache map[string]int
	levelConfigLock   sync.RWMutex
)

func loadConfig(config ConfigLogger) {
	enableConsole = config.EnableConsole
	enableFile = config.EnableFile
	consoleJsonFormat = config.ConsoleJsonFormat
	fileJsonFormat = config.FileJsonFormat
	rootLevel = levelMap[config.RootLevel]
	enableTimestamp = config.EnableTimestamp
	enableFileNameInLog = config.EnableFileNameInLog
	enableStackTrace = config.EnableStackTrace
	logFileLocation = config.LogFileLocation
	logFileSize = config.LogFileSize

	// colon separated key value pairs
	// Example: "router.GA=DEBUG:warehouse.REDSHIFT=DEBUG"
	levelConfigStr := config.LevelConfigStr
	levelConfig = make(map[string]int)
	levelConfigStr = strings.TrimSpace(levelConfigStr)
	if levelConfigStr != "" {
		moduleLevelKVs := strings.Split(levelConfigStr, ":")
		for _, moduleLevelKV := range moduleLevelKVs {
			pair := strings.SplitN(moduleLevelKV, "=", 2)
			if len(pair) < 2 {
				continue
			}
			module := strings.TrimSpace(pair[0])
			if module == "" {
				continue
			}

			levelStr := strings.TrimSpace(pair[1])
			level, ok := levelMap[levelStr]
			if !ok {
				continue
			}
			levelConfig[module] = level
		}
	}
}

var options []zap.Option

func checkAndValidateConfig(configList []interface{}) ConfigLogger {
	if len(configList) != 1 {
		return DefaultConfigLogger
	}
	switch configList[0].(type) {
	case ConfigLogger:
		return configList[0].(ConfigLogger)
	default:
		return DefaultConfigLogger
	}
}

func NewLogger(configList ...interface{}) *LoggerT {
	config := checkAndValidateConfig(configList)
	loadConfig(config)
	return &LoggerT{config: config}
}

// Setup sets up the logger initially
func init() {
	//	loadConfig()
	DefaultConfigLogger = ConfigLogger{EnableConsole: true, EnableFile: false, ConsoleJsonFormat: false, FileJsonFormat: false, LogFileLocation: "/tmp/rudder_log.log", LogFileSize: 100, EnableTimestamp: true, EnableFileNameInLog: false, EnableStackTrace: false, LevelConfigStr: ""}
	loadConfig(DefaultConfigLogger)
	Log = configureLogger()
	loggerLevelsCache = make(map[string]int)
}

func (l *LoggerT) Child(s string) LoggerI {
	if s == "" {
		return l
	}
	copy := *l
	copy.parent = l
	if l.name == "" {
		copy.name = s
	} else {
		copy.name = strings.Join([]string{l.name, s}, ".")
	}
	return &copy
}

func (l *LoggerT) getLoggingLevel() int {
	var found bool
	var level int
	levelConfigLock.RLock()
	if l.name == "" {
		level = levelMap[l.config.RootLevel]
		found = true
	}
	if !found {
		level, found = loggerLevelsCache[l.name]
	}
	if !found {
		level, found = levelConfig[l.name]
	}
	levelConfigLock.RUnlock()
	if found {
		return level
	}

	level = l.parent.getLoggingLevel()

	levelConfigLock.Lock()
	loggerLevelsCache[l.name] = level
	levelConfigLock.Unlock()

	return level
}

// SetModuleLevel sets log level for a module and it's children
// Pass empty string for module parameter for resetting root logging level
func SetModuleLevel(module string, levelStr string) error {
	level, ok := levelMap[levelStr]
	if !ok {
		return errors.New("invalid level value : " + levelStr)
	}
	levelConfigLock.Lock()
	if module == "" {
		rootLevel = level
	} else {
		levelConfig[module] = level
		Log.Info(levelConfig)
	}
	loggerLevelsCache = make(map[string]int)
	levelConfigLock.Unlock()

	return nil
}

//IsDebugLevel Returns true is debug lvl is enabled
func (l *LoggerT) IsDebugLevel() bool {
	return levelDebug >= l.getLoggingLevel()
}

// Debug level logging.
// Most verbose logging level.
func (l *LoggerT) Debug(args ...interface{}) {
	if levelDebug >= l.getLoggingLevel() {
		Log.Debug(args...)
	}
}

// Info level logging.
// Use this to log the state of the application. Dont use Logger.Info in the flow of individual events. Use Logger.Debug instead.
func (l *LoggerT) Info(args ...interface{}) {
	if levelInfo >= l.getLoggingLevel() {
		Log.Info(args...)
	}
}

// Warn level logging.
// Use this to log warnings
func (l *LoggerT) Warn(args ...interface{}) {
	if levelWarn >= l.getLoggingLevel() {
		Log.Warn(args...)
	}
}

// Error level logging.
// Use this to log errors which dont immediately halt the application.
func (l *LoggerT) Error(args ...interface{}) {
	if levelError >= l.getLoggingLevel() {
		Log.Error(args...)
	}
}

// Fatal level logging.
// Use this to log errors which crash the application.
func (l *LoggerT) Fatal(args ...interface{}) {
	if levelFatal >= l.getLoggingLevel() {
		Log.Error(args...)

		//If enableStackTrace is true, Zaplogger will take care of writing stacktrace to the file.
		//Else, we are force writing the stacktrace to the file.
		if !l.config.EnableStackTrace {
			byteArr := make([]byte, 2048)
			n := runtime.Stack(byteArr, false)
			stackTrace := string(byteArr[:n])
			Log.Error(stackTrace)
		}
		Log.Sync()
	}
}

// Debugf does debug level logging similar to fmt.Printf.
// Most verbose logging level
func (l *LoggerT) Debugf(format string, args ...interface{}) {
	if levelDebug >= l.getLoggingLevel() {
		Log.Debugf(format, args...)
	}
}

// Infof does info level logging similar to fmt.Printf.
// Use this to log the state of the application. Dont use Logger.Info in the flow of individual events. Use Logger.Debug instead.
func (l *LoggerT) Infof(format string, args ...interface{}) {
	if levelInfo >= l.getLoggingLevel() {
		Log.Infof(format, args...)
	}
}

// Warnf does warn level logging similar to fmt.Printf.
// Use this to log warnings
func (l *LoggerT) Warnf(format string, args ...interface{}) {
	if levelWarn >= l.getLoggingLevel() {
		Log.Warnf(format, args...)
	}
}

// Errorf does error level logging similar to fmt.Printf.
// Use this to log errors which dont immediately halt the application.
func (l *LoggerT) Errorf(format string, args ...interface{}) {
	if levelError >= l.getLoggingLevel() {
		Log.Errorf(format, args...)
	}
}

// Fatalf does fatal level logging similar to fmt.Printf.
// Use this to log errors which crash the application.
func (l *LoggerT) Fatalf(format string, args ...interface{}) {
	if levelFatal >= l.getLoggingLevel() {
		Log.Errorf(format, args...)

		//If enableStackTrace is true, Zaplogger will take care of writing stacktrace to the file.
		//Else, we are force writing the stacktrace to the file.
		if !l.config.EnableStackTrace {
			byteArr := make([]byte, 2048)
			n := runtime.Stack(byteArr, false)
			stackTrace := string(byteArr[:n])
			Log.Error(stackTrace)
		}
		Log.Sync()
	}
}

// LogRequest reads and logs the request body and resets the body to original state.
func (l *LoggerT) LogRequest(req *http.Request) {
	if levelEvent >= l.getLoggingLevel() {
		defer req.Body.Close()
		bodyBytes, _ := ioutil.ReadAll(req.Body)
		bodyString := string(bodyBytes)
		req.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes))
		//print raw request body for debugging purposes
		Log.Debug("Request Body: ", bodyString)
	}
}

func GetLoggingConfig() map[string]int {
	return loggerLevelsCache
}
