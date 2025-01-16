package utilflag

import (
	"flag"
	"fmt"

	"github.com/spf13/pflag"
)

// Special string used to construct a flag in a subset that expect to be equal to the parent set name.
const EmptyFlagName string = "zzz-empty-flag-name"

func AddSubset(super *pflag.FlagSet, prefix string, subsetFn func(*flag.FlagSet)) {
	compFs := flag.FlagSet{Usage: nil}
	subsetFn(&compFs)

	compFs.VisitAll(func(fl *flag.Flag) {
		if fl.Name == EmptyFlagName {
			fl.Name = prefix
		} else {
			fl.Name = fmt.Sprintf("%s-%s", prefix, fl.Name)
		}

		super.AddGoFlag(fl)
	})
}
