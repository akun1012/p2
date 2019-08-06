package logging

import (
	"fmt"
	"net"

	"github.com/sirupsen/logrus"
)

type SocketHook struct {
	socketPath string
}

func (SocketHook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
		logrus.DebugLevel,
	}
}

var jsonFormatter = new(logrus.JSONFormatter)

func (s SocketHook) Fire(entry *logrus.Entry) error {
	c, err := net.Dial("unix", s.socketPath)
	if err != nil {
		// Airbrake someday
		fmt.Println("Unable to dial socket:", err)
		return nil
	}
	defer c.Close()

	// always use the JSON formatter when logging to a socket. The thing on
	// the other end is a program.
	logMessage, err := jsonFormatter.Format(entry)
	if err != nil {
		// Airbrake someday
		return nil
	}
	_, err = c.Write(logMessage)
	if err != nil {
		// Airbrake someday
		fmt.Println("Unable to write to socket:", err)
	}
	return nil
}
