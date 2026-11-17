package cmd

import (
	"context"
	"io"
	"sync/atomic"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/1set/gut/ystring"
	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/util"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"github.com/vt128/fang/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// presentationOutput forwards command data unchanged. Only Fang's presentation
// phase enables its destination's ANSI policy, so machine output and service
// configuration rendering never pass through a style filter.
type presentationOutput struct {
	output  io.Writer
	profile colorprofile.Profile
	active  atomic.Bool
}

func (w *presentationOutput) Write(data []byte) (int, error) {
	if !w.active.Load() {
		return w.output.Write(data)
	}
	writer := colorprofile.Writer{Forward: w.output, Profile: w.profile}
	return writer.Write(data)
}

func (w *presentationOutput) activate() { w.active.Store(true) }

type presentationTerminal struct {
	*presentationOutput
	fd uintptr
}

func (w *presentationTerminal) Fd() uintptr { return w.fd }

func newPresentationOutput(output io.Writer) (*presentationOutput, io.Writer) {
	w := &presentationOutput{
		output:  output,
		profile: charmColorProfile(util.OutputColorProfile(output)),
	}
	if terminal, ok := output.(interface{ Fd() uintptr }); ok {
		return w, &presentationTerminal{presentationOutput: w, fd: terminal.Fd()}
	}
	return w, w
}

func rootHelpRequested(root *cobra.Command, args []string) bool {
	root.InitDefaultHelpCmd()
	command, remaining, err := root.Find(args)
	if err != nil {
		return false
	}
	if command == root {
		return true
	}
	if command.Name() == "help" {
		target, _, err := root.Find(remaining)
		return err == nil && target == root
	}
	return false
}

// executePresentedCommand leaves cleanup in the application's existing order:
// execute, close hardware, render one final error, then let main choose its exit.
func executePresentedCommand(root *cobra.Command, args []string, cleanup func() error) error {
	stdout, stderr := root.OutOrStdout(), root.ErrOrStderr()
	out, styledOut := newPresentationOutput(stdout)
	errOut, styledErr := newPresentationOutput(stderr)
	root.SetOut(styledOut)
	root.SetErr(styledErr)
	defer root.SetOut(stdout)
	defer root.SetErr(stderr)
	root.SetArgs(args)

	originalLong := root.Long
	if rootHelpRequested(root, args) {
		// Docs generation keeps its source description; only root help carries
		// the existing RoseWater artwork through Fang's actual Long renderer.
		root.Long = util.FormatLogo(config.AppLogoArt, stdout) + ystring.NewLine + originalLong
		defer func() { root.Long = originalLong }()
	}

	var errorWriter io.Writer
	var errorStyles fang.Styles
	ctx := root.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	err := fang.Execute(ctx, root,
		fang.WithoutVersion(), fang.WithoutManpage(),
		fang.WithDarkBackground(true),
		fang.WithColorSchemeFunc(func(background lipgloss.LightDarkFunc) fang.ColorScheme {
			out.activate()
			errOut.activate()
			return fang.DefaultColorScheme(background)
		}),
		fang.WithErrorHandler(func(writer io.Writer, styles fang.Styles, _ error) {
			// Fang resolves its real styles, but cleanup must finish before the
			// final error becomes visible. Do not replace its renderer or exit.
			errorWriter, errorStyles = writer, styles
		}),
	)
	if closeErr := cleanup(); err == nil {
		err = closeErr
	}
	if err == nil {
		return nil
	}
	if errorWriter != nil {
		// Keep user text and diagnostic codes intact instead of title-casing
		// the first word of an identifier or a recovery instruction.
		errorStyles.ErrorText = errorStyles.ErrorText.UnsetTransform()
		fang.DefaultErrorHandler(errorWriter, errorStyles, err)
	} else {
		// A successful command can still fail to close. This is an application
		// diagnostic; it has no preceding Fang error styles to reuse.
		core := newCharmConsoleCore(zapcore.ErrorLevel, stderr, util.OutputColorProfile(stderr), debugMode)
		_ = core.Write(zapcore.Entry{Level: zapcore.ErrorLevel, Time: time.Now(), Message: "could not close device"}, []zapcore.Field{zap.Error(err)})
	}
	return err
}
