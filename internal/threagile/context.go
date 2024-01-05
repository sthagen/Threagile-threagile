package threagile

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt" // TODO: no fmt.Println here
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/threagile/threagile/pkg/model"
	"github.com/threagile/threagile/pkg/security/risks"

	"github.com/threagile/threagile/pkg/common"

	addbuildpipeline "github.com/threagile/threagile/pkg/macros/built-in/add-build-pipeline"
	addvault "github.com/threagile/threagile/pkg/macros/built-in/add-vault"
	prettyprint "github.com/threagile/threagile/pkg/macros/built-in/pretty-print"
	removeunusedtags "github.com/threagile/threagile/pkg/macros/built-in/remove-unused-tags"
	seedrisktracking "github.com/threagile/threagile/pkg/macros/built-in/seed-risk-tracking"
	seedtags "github.com/threagile/threagile/pkg/macros/built-in/seed-tags"

	"gopkg.in/yaml.v3"

	"github.com/threagile/threagile/pkg/docs"
	"github.com/threagile/threagile/pkg/input"
	"github.com/threagile/threagile/pkg/macros"
	"github.com/threagile/threagile/pkg/report"
	"github.com/threagile/threagile/pkg/run"
	"github.com/threagile/threagile/pkg/security/types"
)

type GenerateCommands struct {
	DataFlowDiagram     bool
	DataAssetDiagram    bool
	RisksJSON           bool
	TechnicalAssetsJSON bool
	StatsJSON           bool
	RisksExcel          bool
	TagsExcel           bool
	ReportPDF           bool
}

func (c *GenerateCommands) Defaults() *GenerateCommands {
	*c = GenerateCommands{
		DataFlowDiagram:     true,
		DataAssetDiagram:    true,
		RisksJSON:           true,
		TechnicalAssetsJSON: true,
		StatsJSON:           true,
		RisksExcel:          true,
		TagsExcel:           true,
		ReportPDF:           true,
	}
	return c
}

type Context struct {
	common.Config
	*GenerateCommands

	ServerMode bool
}

func (context *Context) Init() *Context {
	*context = Context{
		GenerateCommands: &GenerateCommands{},
	}

	return context
}

func (context *Context) Defaults(buildTimestamp string) *Context {
	*context = *new(Context).Init()
	context.Config.Defaults(buildTimestamp)
	context.GenerateCommands.Defaults()

	return context
}

func (context *Context) DoIt() {
	progressReporter := common.DefaultProgressReporter{Verbose: context.Config.Verbose}
	defer func() {
		var err error
		if r := recover(); r != nil {
			err = r.(error)
			progressReporter.Info("ERROR: " + err.Error())
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(2)
		}
	}()

	if len(context.Config.ExecuteModelMacro) > 0 {
		fmt.Println(docs.Logo + "\n\n" + docs.VersionText)
	}
	progressReporter.Info("Writing into output directory:", context.Config.OutputFolder)
	progressReporter.Info("Parsing model:", context.Config.InputFile)

	modelInput := *new(input.ModelInput).Defaults()
	loadError := modelInput.Load(context.Config.InputFile)
	if loadError != nil {
		log.Fatal("Unable to load model yaml: ", loadError)
	}

	builtinRiskRules := make(map[string]types.RiskRule)
	for _, rule := range risks.GetBuiltInRiskRules() {
		builtinRiskRules[rule.Category().Id] = rule
	}
	customRiskRules := types.LoadCustomRiskRules(context.Config.RiskRulesPlugins, progressReporter)

	parsedModel, parseError := model.ParseModel(&modelInput, builtinRiskRules, customRiskRules)
	if parseError != nil {
		log.Fatal("Unable to parse model yaml: ", parseError)
	}
	introTextRAA := applyRAA(parsedModel, context.Config.BinFolder, context.RAAPlugin, progressReporter)

	parsedModel.ApplyRiskGeneration(customRiskRules, builtinRiskRules,
		context.Config.SkipRiskRules, progressReporter)
	err := parsedModel.ApplyWildcardRiskTrackingEvaluation(context.Config.IgnoreOrphanedRiskTracking, progressReporter)
	if err != nil {
		// TODO: do not panic and gracefully handle the error
		panic(err)
	}

	err = parsedModel.CheckRiskTracking(context.Config.IgnoreOrphanedRiskTracking, progressReporter)
	if err != nil {
		// TODO: do not panic and gracefully handle the error
		panic(err)
	}

	if len(context.Config.ExecuteModelMacro) > 0 {
		var macroDetails macros.MacroDetails
		switch context.Config.ExecuteModelMacro {
		case addbuildpipeline.GetMacroDetails().ID:
			macroDetails = addbuildpipeline.GetMacroDetails()
		case addvault.GetMacroDetails().ID:
			macroDetails = addvault.GetMacroDetails()
		case prettyprint.GetMacroDetails().ID:
			macroDetails = prettyprint.GetMacroDetails()
		case removeunusedtags.GetMacroDetails().ID:
			macroDetails = removeunusedtags.GetMacroDetails()
		case seedrisktracking.GetMacroDetails().ID:
			macroDetails = seedrisktracking.GetMacroDetails()
		case seedtags.GetMacroDetails().ID:
			macroDetails = seedtags.GetMacroDetails()
		default:
			log.Fatal("Unknown model macro: ", context.Config.ExecuteModelMacro)
		}
		fmt.Println("Executing model macro:", macroDetails.ID)
		fmt.Println()
		fmt.Println()
		context.printBorder(len(macroDetails.Title), true)
		fmt.Println(macroDetails.Title)
		context.printBorder(len(macroDetails.Title), true)
		if len(macroDetails.Description) > 0 {
			fmt.Println(macroDetails.Description)
		}
		fmt.Println()
		reader := bufio.NewReader(os.Stdin)
		var err error
		var nextQuestion macros.MacroQuestion
		for {
			switch macroDetails.ID {
			case addbuildpipeline.GetMacroDetails().ID:
				nextQuestion, err = addbuildpipeline.GetNextQuestion(parsedModel)
			case addvault.GetMacroDetails().ID:
				nextQuestion, err = addvault.GetNextQuestion(parsedModel)
			case prettyprint.GetMacroDetails().ID:
				nextQuestion, err = prettyprint.GetNextQuestion()
			case removeunusedtags.GetMacroDetails().ID:
				nextQuestion, err = removeunusedtags.GetNextQuestion()
			case seedrisktracking.GetMacroDetails().ID:
				nextQuestion, err = seedrisktracking.GetNextQuestion()
			case seedtags.GetMacroDetails().ID:
				nextQuestion, err = seedtags.GetNextQuestion()
			}
			checkErr(err)
			if nextQuestion.NoMoreQuestions() {
				break
			}
			fmt.Println()
			context.printBorder(len(nextQuestion.Title), false)
			fmt.Println(nextQuestion.Title)
			context.printBorder(len(nextQuestion.Title), false)
			if len(nextQuestion.Description) > 0 {
				fmt.Println(nextQuestion.Description)
			}
			resultingMultiValueSelection := make([]string, 0)
			if nextQuestion.IsValueConstrained() {
				if nextQuestion.MultiSelect {
					selectedValues := make(map[string]bool)
					for {
						fmt.Println("Please select (multiple executions possible) from the following values (use number to select/deselect):")
						fmt.Println("    0:", "SELECTION PROCESS FINISHED: CONTINUE TO NEXT QUESTION")
						for i, val := range nextQuestion.PossibleAnswers {
							number := i + 1
							padding, selected := "", " "
							if number < 10 {
								padding = " "
							}
							if val, exists := selectedValues[val]; exists && val {
								selected = "*"
							}
							fmt.Println(" "+selected+" "+padding+strconv.Itoa(number)+":", val)
						}
						fmt.Println()
						fmt.Print("Enter number to select/deselect (or 0 when finished): ")
						answer, err := reader.ReadString('\n')
						// convert CRLF to LF
						answer = strings.TrimSpace(strings.Replace(answer, "\n", "", -1))
						checkErr(err)
						if val, err := strconv.Atoi(answer); err == nil { // flip selection
							if val == 0 {
								for key, selected := range selectedValues {
									if selected {
										resultingMultiValueSelection = append(resultingMultiValueSelection, key)
									}
								}
								break
							} else if val > 0 && val <= len(nextQuestion.PossibleAnswers) {
								selectedValues[nextQuestion.PossibleAnswers[val-1]] = !selectedValues[nextQuestion.PossibleAnswers[val-1]]
							}
						}
					}
				} else {
					fmt.Println("Please choose from the following values (enter value directly or use number):")
					for i, val := range nextQuestion.PossibleAnswers {
						number := i + 1
						padding := ""
						if number < 10 {
							padding = " "
						}
						fmt.Println("   "+padding+strconv.Itoa(number)+":", val)
					}
				}
			}
			message := ""
			validResult := true
			if !nextQuestion.IsValueConstrained() || !nextQuestion.MultiSelect {
				fmt.Println()
				fmt.Println("Enter your answer (use 'BACK' to go one step back or 'QUIT' to quit without executing the model macro)")
				fmt.Print("Answer")
				if len(nextQuestion.DefaultAnswer) > 0 {
					fmt.Print(" (default '" + nextQuestion.DefaultAnswer + "')")
				}
				fmt.Print(": ")
				answer, err := reader.ReadString('\n')
				// convert CRLF to LF
				answer = strings.TrimSpace(strings.Replace(answer, "\n", "", -1))
				checkErr(err)
				if len(answer) == 0 && len(nextQuestion.DefaultAnswer) > 0 { // accepting the default
					answer = nextQuestion.DefaultAnswer
				} else if nextQuestion.IsValueConstrained() { // convert number to value
					if val, err := strconv.Atoi(answer); err == nil {
						if val > 0 && val <= len(nextQuestion.PossibleAnswers) {
							answer = nextQuestion.PossibleAnswers[val-1]
						}
					}
				}
				if strings.ToLower(answer) == "quit" {
					fmt.Println("Quitting without executing the model macro")
					return
				} else if strings.ToLower(answer) == "back" {
					switch macroDetails.ID {
					case addbuildpipeline.GetMacroDetails().ID:
						message, validResult, _ = addbuildpipeline.GoBack()
					case addvault.GetMacroDetails().ID:
						message, validResult, _ = addvault.GoBack()
					case prettyprint.GetMacroDetails().ID:
						message, validResult, _ = prettyprint.GoBack()
					case removeunusedtags.GetMacroDetails().ID:
						message, validResult, _ = removeunusedtags.GoBack()
					case seedrisktracking.GetMacroDetails().ID:
						message, validResult, _ = seedrisktracking.GoBack()
					case seedtags.GetMacroDetails().ID:
						message, validResult, _ = seedtags.GoBack()
					}
				} else if len(answer) > 0 { // individual answer
					if nextQuestion.IsValueConstrained() {
						if !nextQuestion.IsMatchingValueConstraint(answer) {
							fmt.Println()
							fmt.Println(">>> INVALID <<<")
							fmt.Println("Answer does not match any allowed value. Please try again:")
							continue
						}
					}
					switch macroDetails.ID {
					case addbuildpipeline.GetMacroDetails().ID:
						message, validResult, _ = addbuildpipeline.ApplyAnswer(nextQuestion.ID, answer)
					case addvault.GetMacroDetails().ID:
						message, validResult, _ = addvault.ApplyAnswer(nextQuestion.ID, answer)
					case prettyprint.GetMacroDetails().ID:
						message, validResult, _ = prettyprint.ApplyAnswer(nextQuestion.ID, answer)
					case removeunusedtags.GetMacroDetails().ID:
						message, validResult, _ = removeunusedtags.ApplyAnswer(nextQuestion.ID, answer)
					case seedrisktracking.GetMacroDetails().ID:
						message, validResult, _ = seedrisktracking.ApplyAnswer(nextQuestion.ID, answer)
					case seedtags.GetMacroDetails().ID:
						message, validResult, _ = seedtags.ApplyAnswer(nextQuestion.ID, answer)
					}
				}
			} else {
				switch macroDetails.ID {
				case addbuildpipeline.GetMacroDetails().ID:
					message, validResult, err = addbuildpipeline.ApplyAnswer(nextQuestion.ID, resultingMultiValueSelection...)
				case addvault.GetMacroDetails().ID:
					message, validResult, err = addvault.ApplyAnswer(nextQuestion.ID, resultingMultiValueSelection...)
				case prettyprint.GetMacroDetails().ID:
					message, validResult, err = prettyprint.ApplyAnswer(nextQuestion.ID, resultingMultiValueSelection...)
				case removeunusedtags.GetMacroDetails().ID:
					message, validResult, err = removeunusedtags.ApplyAnswer(nextQuestion.ID, resultingMultiValueSelection...)
				case seedrisktracking.GetMacroDetails().ID:
					message, validResult, err = seedrisktracking.ApplyAnswer(nextQuestion.ID, resultingMultiValueSelection...)
				case seedtags.GetMacroDetails().ID:
					message, validResult, err = seedtags.ApplyAnswer(nextQuestion.ID, resultingMultiValueSelection...)
				}
			}
			checkErr(err)
			if !validResult {
				fmt.Println()
				fmt.Println(">>> INVALID <<<")
			}
			fmt.Println(message)
			fmt.Println()
		}
		for {
			fmt.Println()
			fmt.Println()
			fmt.Println("#################################################################")
			fmt.Println("Do you want to execute the model macro (updating the model file)?")
			fmt.Println("#################################################################")
			fmt.Println()
			fmt.Println("The following changes will be applied:")
			var changes []string
			message := ""
			validResult := true
			var err error
			switch macroDetails.ID {
			case addbuildpipeline.GetMacroDetails().ID:
				changes, message, validResult, err = addbuildpipeline.GetFinalChangeImpact(&modelInput, parsedModel)
			case addvault.GetMacroDetails().ID:
				changes, message, validResult, err = addvault.GetFinalChangeImpact(&modelInput, parsedModel)
			case prettyprint.GetMacroDetails().ID:
				changes, message, validResult, err = prettyprint.GetFinalChangeImpact(&modelInput)
			case removeunusedtags.GetMacroDetails().ID:
				changes, message, validResult, err = removeunusedtags.GetFinalChangeImpact(&modelInput)
			case seedrisktracking.GetMacroDetails().ID:
				changes, message, validResult, err = seedrisktracking.GetFinalChangeImpact(&modelInput)
			case seedtags.GetMacroDetails().ID:
				changes, message, validResult, err = seedtags.GetFinalChangeImpact(&modelInput)
			}
			checkErr(err)
			for _, change := range changes {
				fmt.Println(" -", change)
			}
			if !validResult {
				fmt.Println()
				fmt.Println(">>> INVALID <<<")
			}
			fmt.Println()
			fmt.Println(message)
			fmt.Println()
			fmt.Print("Apply these changes to the model file?\nType Yes or No: ")
			answer, err := reader.ReadString('\n')
			// convert CRLF to LF
			answer = strings.TrimSpace(strings.Replace(answer, "\n", "", -1))
			checkErr(err)
			answer = strings.ToLower(answer)
			fmt.Println()
			if answer == "yes" || answer == "y" {
				message := ""
				validResult := true
				var err error
				switch macroDetails.ID {
				case addbuildpipeline.GetMacroDetails().ID:
					message, validResult, err = addbuildpipeline.Execute(&modelInput, parsedModel)
				case addvault.GetMacroDetails().ID:
					message, validResult, err = addvault.Execute(&modelInput, parsedModel)
				case prettyprint.GetMacroDetails().ID:
					message, validResult, err = prettyprint.Execute(&modelInput)
				case removeunusedtags.GetMacroDetails().ID:
					message, validResult, err = removeunusedtags.Execute(&modelInput, parsedModel)
				case seedrisktracking.GetMacroDetails().ID:
					message, validResult, err = seedrisktracking.Execute(parsedModel, &modelInput)
				case seedtags.GetMacroDetails().ID:
					message, validResult, err = seedtags.Execute(&modelInput, parsedModel)
				}
				checkErr(err)
				if !validResult {
					fmt.Println()
					fmt.Println(">>> INVALID <<<")
				}
				fmt.Println(message)
				fmt.Println()
				backupFilename := context.Config.InputFile + ".backup"
				fmt.Println("Creating backup model file:", backupFilename) // TODO add random files in /dev/shm space?
				_, err = copyFile(context.Config.InputFile, backupFilename)
				checkErr(err)
				fmt.Println("Updating model")
				yamlBytes, err := yaml.Marshal(modelInput)
				checkErr(err)
				/*
					yamlBytes = model.ReformatYAML(yamlBytes)
				*/
				fmt.Println("Writing model file:", context.Config.InputFile)
				err = os.WriteFile(context.Config.InputFile, yamlBytes, 0400)
				checkErr(err)
				fmt.Println("Model file successfully updated")
				return
			} else if answer == "no" || answer == "n" {
				fmt.Println("Quitting without executing the model macro")
				return
			}
		}
	}

	if context.GenerateCommands.ReportPDF { // as the PDF report includes both diagrams
		context.GenerateCommands.DataFlowDiagram = true
		context.GenerateCommands.DataAssetDiagram = true
	}

	diagramDPI := context.Config.DiagramDPI
	if diagramDPI < common.MinGraphvizDPI {
		diagramDPI = common.MinGraphvizDPI
	} else if diagramDPI > common.MaxGraphvizDPI {
		diagramDPI = common.MaxGraphvizDPI
	}
	// Data-flow Diagram rendering
	if context.GenerateCommands.DataFlowDiagram {
		gvFile := filepath.Join(context.Config.OutputFolder, context.Config.DataFlowDiagramFilenameDOT)
		if !context.Config.KeepDiagramSourceFiles {
			tmpFileGV, err := os.CreateTemp(context.Config.TempFolder, context.Config.DataFlowDiagramFilenameDOT)
			checkErr(err)
			gvFile = tmpFileGV.Name()
			defer func() { _ = os.Remove(gvFile) }()
		}
		dotFile := report.WriteDataFlowDiagramGraphvizDOT(parsedModel, gvFile, diagramDPI, context.Config.AddModelTitle, progressReporter)

		err := report.GenerateDataFlowDiagramGraphvizImage(dotFile, context.Config.OutputFolder,
			context.Config.TempFolder, context.Config.BinFolder, context.Config.DataFlowDiagramFilenamePNG, progressReporter)
		if err != nil {
			fmt.Println(err)
		}
	}
	// Data Asset Diagram rendering
	if context.GenerateCommands.DataAssetDiagram {
		gvFile := filepath.Join(context.Config.OutputFolder, context.Config.DataAssetDiagramFilenameDOT)
		if !context.Config.KeepDiagramSourceFiles {
			tmpFile, err := os.CreateTemp(context.Config.TempFolder, context.Config.DataAssetDiagramFilenameDOT)
			checkErr(err)
			gvFile = tmpFile.Name()
			defer func() { _ = os.Remove(gvFile) }()
		}
		dotFile := report.WriteDataAssetDiagramGraphvizDOT(parsedModel, gvFile, diagramDPI, progressReporter)
		err := report.GenerateDataAssetDiagramGraphvizImage(dotFile, context.Config.OutputFolder,
			context.Config.TempFolder, context.Config.BinFolder, context.Config.DataAssetDiagramFilenamePNG, progressReporter)
		if err != nil {
			fmt.Println(err)
		}
	}

	// risks as risks json
	if context.GenerateCommands.RisksJSON {
		if context.Config.Verbose {
			fmt.Println("Writing risks json")
		}
		report.WriteRisksJSON(parsedModel, filepath.Join(context.Config.OutputFolder, context.Config.JsonRisksFilename))
	}

	// technical assets json
	if context.GenerateCommands.TechnicalAssetsJSON {
		if context.Config.Verbose {
			fmt.Println("Writing technical assets json")
		}
		report.WriteTechnicalAssetsJSON(parsedModel, filepath.Join(context.Config.OutputFolder, context.Config.JsonTechnicalAssetsFilename))
	}

	// risks as risks json
	if context.GenerateCommands.StatsJSON {
		if context.Config.Verbose {
			fmt.Println("Writing stats json")
		}
		report.WriteStatsJSON(parsedModel, filepath.Join(context.Config.OutputFolder, context.Config.JsonStatsFilename))
	}

	// risks Excel
	if context.GenerateCommands.RisksExcel {
		if context.Config.Verbose {
			fmt.Println("Writing risks excel")
		}
		report.WriteRisksExcelToFile(parsedModel, filepath.Join(context.Config.OutputFolder, context.Config.ExcelRisksFilename))
	}

	// tags Excel
	if context.GenerateCommands.TagsExcel {
		if context.Config.Verbose {
			fmt.Println("Writing tags excel")
		}
		report.WriteTagsExcelToFile(parsedModel, filepath.Join(context.Config.OutputFolder, context.Config.ExcelTagsFilename))
	}

	if context.GenerateCommands.ReportPDF {
		// hash the YAML input file
		f, err := os.Open(context.Config.InputFile)
		checkErr(err)
		defer func() { _ = f.Close() }()
		hasher := sha256.New()
		if _, err := io.Copy(hasher, f); err != nil {
			panic(err)
		}
		modelHash := hex.EncodeToString(hasher.Sum(nil))
		// report PDF
		if context.Config.Verbose {
			fmt.Println("Writing report pdf")
		}
		report.WriteReportPDF(filepath.Join(context.Config.OutputFolder, context.Config.ReportFilename),
			filepath.Join(context.Config.AppFolder, context.Config.TemplateFilename),
			filepath.Join(context.Config.OutputFolder, context.Config.DataFlowDiagramFilenamePNG),
			filepath.Join(context.Config.OutputFolder, context.Config.DataAssetDiagramFilenamePNG),
			context.Config.InputFile,
			context.Config.SkipRiskRules,
			context.Config.BuildTimestamp,
			modelHash,
			introTextRAA,
			customRiskRules,
			context.Config.TempFolder,
			parsedModel)
	}
}

func copyFile(src, dst string) (int64, error) {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer func() { _ = source.Close() }()

	destination, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer func() { _ = destination.Close() }()
	nBytes, err := io.Copy(destination, source)
	return nBytes, err
}

func (context *Context) printBorder(length int, bold bool) {
	char := "-"
	if bold {
		char = "="
	}
	for i := 1; i <= length; i++ {
		fmt.Print(char)
	}
	fmt.Println()
}

func applyRAA(parsedModel *types.ParsedModel, binFolder, raaPlugin string, progressReporter common.DefaultProgressReporter) string {
	progressReporter.Info("Applying RAA calculation:", raaPlugin)

	runner, loadError := new(run.Runner).Load(filepath.Join(binFolder, raaPlugin))
	if loadError != nil {
		progressReporter.Warn(fmt.Sprintf("WARNING: raa %q not loaded: %v\n", raaPlugin, loadError))
		return ""
	}

	runError := runner.Run(parsedModel, parsedModel)
	if runError != nil {
		progressReporter.Warn(fmt.Sprintf("WARNING: raa %q not applied: %v\n", raaPlugin, runError))
		return ""
	}

	return runner.ErrorOutput
}

func checkErr(err error) {
	if err != nil {
		panic(err)
	}
}

// TODO: remove from here as soon as moved to cobra, here is only for a backward compatibility
// this file supposed to be only about the logic
func userHomeDir() string {
	switch runtime.GOOS {
	case "windows":
		home := os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		return home

	default:
		return os.Getenv("HOME")
	}
}

func expandPath(path string) string {
	home := userHomeDir()
	if strings.HasPrefix(path, "~") {
		path = strings.Replace(path, "~", home, 1)
	}

	if strings.HasPrefix(path, "$HOME") {
		path = strings.Replace(path, "$HOME", home, -1)
	}

	return path
}

func (context *Context) ParseCommandlineArgs() *Context {
	configFile := flag.String("config", "", "config file")
	configError := context.Config.Load(*configFile)
	if configError != nil {
		fmt.Printf("WARNING: failed to load config file %q: %v\n", *configFile, configError)
	}

	// folders
	flag.StringVar(&context.Config.AppFolder, "app-dir", common.AppDir, "app folder (default: "+common.AppDir+")")
	flag.StringVar(&context.Config.ServerFolder, "server-dir", common.DataDir, "base folder for server mode (default: "+common.DataDir+")")
	flag.StringVar(&context.Config.TempFolder, "temp-dir", common.TempDir, "temporary folder location")
	flag.StringVar(&context.Config.BinFolder, "bin-dir", common.BinDir, "binary folder location")
	flag.StringVar(&context.Config.OutputFolder, "output", ".", "output directory")

	// files
	flag.StringVar(&context.Config.InputFile, "model", common.InputFile, "input model yaml file")
	flag.StringVar(&context.RAAPlugin, "raa-run", "raa_calc", "RAA calculation run file name")

	// flags / parameters
	flag.BoolVar(&context.Config.Verbose, "verbose", false, "verbose output")
	flag.IntVar(&context.Config.DiagramDPI, "diagram-dpi", context.Config.DiagramDPI, "DPI used to render: maximum is "+strconv.Itoa(context.Config.MaxGraphvizDPI)+"")
	flag.StringVar(&context.Config.SkipRiskRules, "skip-risk-rules", "", "comma-separated list of risk rules (by their ID) to skip")
	flag.BoolVar(&context.Config.IgnoreOrphanedRiskTracking, "ignore-orphaned-risk-tracking", false, "ignore orphaned risk tracking (just log them) not matching a concrete risk")
	flag.IntVar(&context.Config.ServerPort, "server", 0, "start a server (instead of commandline execution) on the given port")
	flag.StringVar(&context.Config.ExecuteModelMacro, "execute-model-macro", "", "Execute model macro (by ID)")
	flag.StringVar(&context.Config.TemplateFilename, "background", "background.pdf", "background pdf file")
	riskRulesPlugins := flag.String("custom-risk-rules-plugins", "", "comma-separated list of plugins file names with custom risk rules to load")
	context.Config.RiskRulesPlugins = strings.Split(*riskRulesPlugins, ",")

	// commands
	flag.BoolVar(&context.GenerateCommands.DataFlowDiagram, "generate-data-flow-diagram", true, "generate data-flow diagram")
	flag.BoolVar(&context.GenerateCommands.DataAssetDiagram, "generate-data-asset-diagram", true, "generate data asset diagram")
	flag.BoolVar(&context.GenerateCommands.RisksJSON, "generate-risks-json", true, "generate risks json")
	flag.BoolVar(&context.GenerateCommands.StatsJSON, "generate-stats-json", true, "generate stats json")
	flag.BoolVar(&context.GenerateCommands.TechnicalAssetsJSON, "generate-technical-assets-json", true, "generate technical assets json")
	flag.BoolVar(&context.GenerateCommands.RisksExcel, "generate-risks-excel", true, "generate risks excel")
	flag.BoolVar(&context.GenerateCommands.TagsExcel, "generate-tags-excel", true, "generate tags excel")
	flag.BoolVar(&context.GenerateCommands.ReportPDF, "generate-report-pdf", true, "generate report pdf, including diagrams")

	flag.Usage = func() {
		fmt.Println(docs.Logo + "\n\n" + docs.VersionText)
		_, _ = fmt.Fprintf(os.Stderr, "Usage: threagile [options]")
		fmt.Println()
	}
	flag.Parse()

	context.Config.InputFile = expandPath(context.Config.InputFile)
	context.Config.AppFolder = expandPath(context.Config.AppFolder)
	context.Config.ServerFolder = expandPath(context.Config.ServerFolder)
	context.Config.TempFolder = expandPath(context.Config.TempFolder)
	context.Config.BinFolder = expandPath(context.Config.BinFolder)
	context.Config.OutputFolder = expandPath(context.Config.OutputFolder)

	context.ServerMode = context.Config.ServerPort > 0

	return context
}
