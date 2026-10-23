package telegram

import (
	"context"

	"github.com/gotd/log"
	"github.com/rs/zerolog"
)

// zlog sends gotd's log records to dmz.log.
type zlog struct{ l zerolog.Logger }

func zeroLevel(lv log.Level) zerolog.Level {
	switch {
	case lv < log.LevelInfo:
		return zerolog.DebugLevel
	case lv < log.LevelWarn:
		return zerolog.InfoLevel
	case lv < log.LevelError:
		return zerolog.WarnLevel
	}
	return zerolog.ErrorLevel
}

func (z zlog) Enabled(_ context.Context, lv log.Level) bool { return zeroLevel(lv) >= z.l.GetLevel() }

func (z zlog) Log(_ context.Context, lv log.Level, msg string, attrs ...log.Attr) {
	e := z.l.WithLevel(zeroLevel(lv)).Str("component", "gotd")
	for _, a := range attrs {
		e = e.Str(a.Key, a.Value.String())
	}
	e.Msg(msg)
}
