package cmd

import (
	"sort"
	"strings"

	"bitbucket.org/ai69/amoy"
	"github.com/spf13/cobra"
)

// configCmd represents the config command
var configCmd = &cobra.Command{
	Use:     "config",
	Aliases: aliasesConfig,
	Short:   "Manage configuration",
	Long: hdoc(`
		Modify and review configuration files using subcommands like set, get, and list.
	`),
}

func init() {
	rootCmd.AddCommand(configCmd)

	// Configuration settings

	// Persistent flags which will work for this command and all subcommands
	// configCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Local flags which will only run when this command is called directly
	// configCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

type configKeyValuePair struct {
	Key   string
	Value interface{}
}

type configKeyValuePairList []configKeyValuePair

func (l configKeyValuePairList) Len() int {
	return len(l)
}

func (l configKeyValuePairList) Sort() {
	sort.SliceStable(l, func(i, j int) bool {
		return l[i].Key < l[j].Key
	})
}

func (l configKeyValuePairList) String() string {
	var sb strings.Builder
	for _, kv := range l {
		sb.WriteString(kv.Key)
		sb.WriteString("=")
		sb.WriteString(amoy.ToOneLineJSON(kv.Value))
		sb.WriteString("\n")
	}
	return sb.String()
}
