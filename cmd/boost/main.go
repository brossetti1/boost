package main

import (
	"fmt"
	"os"

	logging "github.com/ipfs/go-log/v2"
	"github.com/urfave/cli/v2"

	"github.com/filecoin-project/boost/build"
	cliutil "github.com/filecoin-project/boost/cli/util"
)

var log = logging.Logger("boost")

const (
	FlagBoostRepo = "boost-repo"
)

func main() {
	_ = logging.SetLogLevel("boost", "INFO")

	app := &cli.App{
		Name:                 "boost",
		Usage:                "Filecoin Markets V2 MVP",
		EnableBashCompletion: true,
		Version:              build.UserVersion(),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    FlagBoostRepo,
				EnvVars: []string{"BOOST_PATH"},
				Usage:   fmt.Sprintf("boost repo path"),
				Value:   "~/.boost",
			},
			cliutil.FlagVeryVerbose,
		},
		Commands: []*cli.Command{
			runCmd,
			initCmd,
		},
	}
	app.Setup()

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func before(cctx *cli.Context) error {
	if cliutil.IsVeryVerbose {
		_ = logging.SetLogLevel("boost", "DEBUG")
	}

	return nil
}