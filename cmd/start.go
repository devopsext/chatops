package cmd

import (
	"github.com/devopsext/chatops/processor"
	"github.com/spf13/pflag"
)

var startOptions = processor.StartOptions{
	Template: envFileContentExpand("START_TEMPLATE", ""),
}

func SetStartFlags(flags *pflag.FlagSet) {

	flags.StringVar(&startOptions.Template, "start-template", startOptions.Template, "Start template")
}
