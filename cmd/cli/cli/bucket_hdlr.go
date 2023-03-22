// Package cli provides easy-to-use commands to manage, monitor, and utilize AIS clusters.
// This file handles CLI commands that pertain to AIS buckets.
/*
 * Copyright (c) 2021-2023, NVIDIA CORPORATION. All rights reserved.
 */
package cli

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/NVIDIA/aistore/api"
	"github.com/NVIDIA/aistore/api/apc"
	"github.com/NVIDIA/aistore/cmd/cli/teb"
	"github.com/NVIDIA/aistore/cmn"
	"github.com/NVIDIA/aistore/cmn/cos"
	"github.com/urfave/cli"
)

const (
	errFmtSameBucket = "cannot %s bucket %q onto itself"
	errFmtExclusive  = "flags %s and %s are mutually exclusive"
)

var examplesBckSetProps = `
Usage examples:
- ais bucket props set [BUCKET] checksum.type=xxhash
- ais bucket props set ais://nnn checksum.type=md5 checksum.validate_warm_get=true
- ais bucket props [BUCKET] checksum
  (see docs/cli for details)
`

var (
	// flags
	bucketCmdsFlags = map[string][]cli.Flag{
		commandCreate: {
			ignoreErrorFlag,
			bucketPropsFlag,
			forceFlag,
		},
		commandRemove: {
			ignoreErrorFlag,
			yesFlag,
		},
		commandCopy: {
			copyAllObjsFlag,
			continueOnErrorFlag,
			forceFlag,
			copyDryRunFlag,
			copyPrependFlag,
			copyObjPrefixFlag,
			listFlag,
			templateFlag,
			progressFlag,
			refreshFlag,
			waitFlag,
			waitJobXactFinishedFlag,
		},
		commandRename: {
			waitFlag,
			waitJobXactFinishedFlag,
		},
		commandEvict: append(
			listrangeFlags,
			dryRunFlag,
			keepMDFlag,
			verboseFlag,
		),
		cmdSetBprops: {
			forceFlag,
		},
		cmdResetBprops: {},

		commandList: {
			allObjsOrBcksFlag,
			listObjCachedFlag,
			nameOnlyFlag,
			objPropsFlag,
			regexLsAnyFlag,
			templateFlag,
			listObjPrefixFlag,
			pageSizeFlag,
			pagedFlag,
			objLimitFlag,
			showUnmatchedFlag,
			noHeaderFlag,
			noFooterFlag,
			maxPagesFlag,
			startAfterFlag,
			bckSummaryFlag,
			listAnonymousFlag,
			listArchFlag,
			unitsFlag,
		},

		cmdLRU: {
			enableFlag,
			disableFlag,
		},
	}

	bckSummaryFlags = append(storageSummFlags, validateSummaryFlag)

	// commands
	bucketsObjectsCmdList = cli.Command{
		Name:         commandList,
		Usage:        "list buckets, objects in buckets, and files in objects formatted as archives",
		ArgsUsage:    listAnyCommandArgument,
		Flags:        bucketCmdsFlags[commandList],
		Action:       listAnyHandler,
		BashComplete: bucketCompletions(bcmplop{}),
	}

	bucketCmdSummary = cli.Command{
		Name:         cmdSummary,
		Usage:        "generate and display bucket summary",
		ArgsUsage:    optionalBucketArgument,
		Flags:        bckSummaryFlags,
		Action:       summaryBucketHandler,
		BashComplete: bucketCompletions(bcmplop{}),
	}

	bucketCmdLRU = cli.Command{
		Name:         cmdLRU,
		Usage:        "show bucket's LRU configuration; enable or disable LRU eviction",
		ArgsUsage:    optionalBucketArgument,
		Flags:        bucketCmdsFlags[cmdLRU],
		Action:       lruBucketHandler,
		BashComplete: bucketCompletions(bcmplop{}),
	}
	bucketObjCmdEvict = cli.Command{
		Name:         commandEvict,
		Usage:        "evict all (default) or selected objects from remote bucket (to select, use '--list' or '--template')",
		ArgsUsage:    optionalObjectsArgument,
		Flags:        bucketCmdsFlags[commandEvict],
		Action:       evictHandler,
		BashComplete: bucketCompletions(bcmplop{multiple: true}),
	}
	bucketCmdCopy = cli.Command{
		Name:         commandCopy,
		Usage:        "copy entire bucket or selected objects (to select, use '--list' or '--template')",
		ArgsUsage:    bucketSrcArgument + " " + bucketDstArgument,
		Flags:        bucketCmdsFlags[commandCopy],
		Action:       copyBucketHandler,
		BashComplete: manyBucketsCompletions([]cli.BashCompleteFunc{}, 0, 2),
	}
	bucketCmdRename = cli.Command{
		Name:         commandRename,
		Usage:        "rename/move ais bucket",
		ArgsUsage:    bucketArgument + " " + bucketNewArgument,
		Flags:        bucketCmdsFlags[commandRename],
		Action:       mvBucketHandler,
		BashComplete: manyBucketsCompletions([]cli.BashCompleteFunc{}, 0, 2),
	}

	bucketCmd = cli.Command{
		Name:  commandBucket,
		Usage: "create/destroy buckets, list bucket contents, show existing buckets and their properties",
		Subcommands: []cli.Command{
			bucketsObjectsCmdList,
			bucketCmdSummary,
			bucketCmdLRU,
			bucketObjCmdEvict,
			makeAlias(showCmdBucket, "", true, commandShow), // alias for `ais show`
			{
				Name:      commandCreate,
				Usage:     "create ais buckets",
				ArgsUsage: bucketsArgument,
				Flags:     bucketCmdsFlags[commandCreate],
				Action:    createBucketHandler,
			},
			bucketCmdCopy,
			bucketCmdRename,
			{
				Name:      commandRemove,
				Usage:     "remove ais buckets",
				ArgsUsage: bucketsArgument,
				Flags:     bucketCmdsFlags[commandRemove],
				Action:    removeBucketHandler,
				BashComplete: bucketCompletions(bcmplop{
					multiple: true, provider: apc.AIS,
				}),
			},
			{
				Name:   cmdProps,
				Usage:  "show, update or reset bucket properties",
				Action: showBckPropsHandler,
				Subcommands: []cli.Command{
					{
						Name:      cmdSetBprops,
						Usage:     "update bucket properties",
						ArgsUsage: bucketPropsArgument,
						Flags:     bucketCmdsFlags[cmdSetBprops],
						Action:    setPropsHandler,
						BashComplete: bucketCompletions(
							bcmplop{additionalCompletions: []cli.BashCompleteFunc{bpropCompletions}},
						),
					},
					{
						Name:      cmdResetBprops,
						Usage:     "reset bucket properties",
						ArgsUsage: bucketPropsArgument,
						Flags:     bucketCmdsFlags[cmdResetBprops],
						Action:    resetPropsHandler,
						BashComplete: bucketCompletions(
							bcmplop{additionalCompletions: []cli.BashCompleteFunc{bpropCompletions}},
						),
					},
					makeAlias(showCmdBucket, "", true, commandShow),
				},
			},
		},
	}
)

func createBucketHandler(c *cli.Context) (err error) {
	var props *cmn.BucketPropsToUpdate
	if flagIsSet(c, bucketPropsFlag) {
		propSingleBck, err := parseBckPropsFromContext(c)
		if err != nil {
			return err
		}
		props = propSingleBck
		props.Force = flagIsSet(c, forceFlag)
	}
	buckets, err := bucketsFromArgsOrEnv(c)
	if err != nil {
		return err
	}
	for _, bck := range buckets {
		if err := createBucket(c, bck, props); err != nil {
			return err
		}
	}
	return nil
}

func checkObjectHealth(queryBcks cmn.QueryBcks) error {
	type bucketHealth struct {
		Bck           cmn.Bck
		ObjectCnt     uint64
		Misplaced     uint64
		MissingCopies uint64
	}
	bcks, err := api.ListBuckets(apiBP, queryBcks, apc.FltPresent)
	if err != nil {
		return err
	}
	bckSums := make([]*bucketHealth, 0)
	msg := &apc.LsoMsg{Flags: apc.LsAll}
	msg.AddProps(apc.GetPropsCopies, apc.GetPropsCached)

	for _, bck := range bcks {
		if queryBcks.Name != "" && !queryBcks.Equal(&bck) {
			continue
		}
		var (
			objList *cmn.LsoResult
			obj     *cmn.LsoEntry
		)
		p, err := headBucket(bck, true /* don't add */)
		if err != nil {
			return err
		}
		copies := int16(p.Mirror.Copies)
		stats := &bucketHealth{Bck: bck}
		objList, err = api.ListObjects(apiBP, bck, msg, 0)
		if err != nil {
			return err
		}

		updateStats := func(obj *cmn.LsoEntry) {
			if obj == nil {
				return
			}
			stats.ObjectCnt++
			if !obj.IsStatusOK() {
				stats.Misplaced++
			} else if obj.CheckExists() && p.Mirror.Enabled && obj.Copies < copies {
				stats.MissingCopies++
			}
		}

		for _, entry := range objList.Entries {
			if obj == nil {
				obj = entry
				continue
			}
			if obj.Name == entry.Name {
				if entry.IsStatusOK() {
					obj = entry
				}
				continue
			}
			updateStats(obj)
			obj = entry
		}
		updateStats(obj)

		bckSums = append(bckSums, stats)
	}
	return teb.Print(bckSums, teb.BucketSummaryValidateTmpl)
}

func summaryBucketHandler(c *cli.Context) (err error) {
	if flagIsSet(c, validateSummaryFlag) {
		return showMisplacedAndMore(c)
	}

	return summaryStorageHandler(c)
}

func showMisplacedAndMore(c *cli.Context) (err error) {
	queryBcks, err := parseQueryBckURI(c, c.Args().First())
	if err != nil {
		return err
	}

	fValidate := func() error {
		return checkObjectHealth(queryBcks)
	}
	return cmn.WaitForFunc(fValidate, longClientTimeout)
}

func mvBucketHandler(c *cli.Context) error {
	bckFrom, bckTo, err := parseBcks(c, bucketArgument, bucketNewArgument, 0 /*shift*/)
	if err != nil {
		return err
	}
	if bckFrom.Equal(&bckTo) {
		return incorrectUsageMsg(c, errFmtSameBucket, commandRename, bckTo)
	}
	return mvBucket(c, bckFrom, bckTo)
}

func removeBucketHandler(c *cli.Context) (err error) {
	var buckets []cmn.Bck
	if buckets, err = bucketsFromArgsOrEnv(c); err != nil {
		return
	}
	return destroyBuckets(c, buckets)
}

func evictHandler(c *cli.Context) (err error) {
	printDryRunHeader(c)

	if c.NArg() == 0 {
		return missingArgumentsError(c, c.Command.ArgsUsage)
	}

	// Bucket argument provided by the user.
	if c.NArg() == 1 {
		uri := c.Args().Get(0)
		bck, objName, err := parseBckObjectURI(c, uri, true)
		if err != nil {
			return err
		}
		if flagIsSet(c, listFlag) || flagIsSet(c, templateFlag) {
			if objName != "" {
				return incorrectUsageMsg(c,
					"object name (%q) cannot be used together with %s and/or %s flags",
					objName, qflprn(listFlag), qflprn(templateFlag))
			}
			// List or range operation on a given bucket.
			return listrange(c, bck)
		}
		if objName == "" {
			// Evict entire bucket.
			return evictBucket(c, bck)
		}

		// Evict a single object from remote bucket - multiObjOp will handle.
	}

	// List and range flags are invalid with object argument(s)
	if flagIsSet(c, listFlag) || flagIsSet(c, templateFlag) {
		return incorrectUsageMsg(c, "flags %q, %q cannot be used together with object name arguments",
			listFlag.Name, templateFlag.Name)
	}

	// operation on a given object or objects.
	return multiobjArg(c, commandEvict)
}

func resetPropsHandler(c *cli.Context) error {
	bck, err := parseBckURI(c, c.Args().First(), true /*require provider*/)
	if err != nil {
		return err
	}
	if _, err := api.ResetBucketProps(apiBP, bck); err != nil {
		return err
	}
	actionDone(c, "Bucket props successfully reset to cluster defaults")
	return nil
}

func lruBucketHandler(c *cli.Context) error {
	bck, err := parseBckURI(c, c.Args().First(), true /*require provider*/)
	if err != nil {
		return err
	}
	var p *cmn.BucketProps
	if p, err = headBucket(bck, true /* don't add */); err != nil {
		return err
	}
	defProps, err := defaultBckProps(bck)
	if err != nil {
		return err
	}
	if flagIsSet(c, enableFlag) {
		return toggleLRU(c, bck, p, true)
	}
	if flagIsSet(c, disableFlag) {
		return toggleLRU(c, bck, p, false)
	}
	return HeadBckTable(c, p, defProps, "lru")
}

func toggleLRU(c *cli.Context, bck cmn.Bck, p *cmn.BucketProps, toggle bool) (err error) {
	const fmts = "Bucket %q: LRU is already %s, nothing to do\n"
	if toggle && p.LRU.Enabled {
		fmt.Fprintf(c.App.Writer, fmts, bck.Cname(""), "enabled")
		return
	}
	if !toggle && !p.LRU.Enabled {
		fmt.Fprintf(c.App.Writer, fmts, bck.Cname(""), "disabled")
		return
	}
	toggledProps, err := cmn.NewBucketPropsToUpdate(cos.StrKVs{"lru.enabled": strconv.FormatBool(toggle)})
	if err != nil {
		return
	}
	return updateBckProps(c, bck, p, toggledProps)
}

func setPropsHandler(c *cli.Context) (err error) {
	var currProps *cmn.BucketProps
	bck, err := parseBckURI(c, c.Args().First(), true /*require provider*/)
	if err != nil {
		return err
	}
	if currProps, err = headBucket(bck, false /* don't add */); err != nil {
		return err
	}
	newProps, err := parseBckPropsFromContext(c)
	if err != nil {
		if strings.Contains(err.Error(), "missing property") {
			if errV := showBucketProps(c); errV == nil {
				msg := err.Error() + examplesBckSetProps
				fmt.Fprintln(c.App.ErrWriter)
				actionWarn(c, msg)
				return nil
			}
		}
		return fmt.Errorf("%v%s", err, examplesBckSetProps)
	}
	newProps.Force = flagIsSet(c, forceFlag)

	return updateBckProps(c, bck, currProps, newProps)
}

func updateBckProps(c *cli.Context, bck cmn.Bck, currProps *cmn.BucketProps, updateProps *cmn.BucketPropsToUpdate) (err error) {
	// Apply updated props and check for change
	allNewProps := currProps.Clone()
	allNewProps.Apply(updateProps)
	if allNewProps.Equal(currProps) {
		displayPropsEqMsg(c, bck)
		return nil
	}
	if _, err = api.SetBucketProps(apiBP, bck, updateProps); err != nil {
		if herr, ok := err.(*cmn.ErrHTTP); ok && herr.Status == http.StatusNotFound {
			return herr
		}
		helpMsg := fmt.Sprintf("To show bucket properties, run '%s %s %s %s'",
			cliName, commandShow, cmdBucket, bck.Cname(""))
		return newAdditionalInfoError(err, helpMsg)
	}
	showDiff(c, currProps, allNewProps)
	actionDone(c, "\nBucket props successfully updated.")
	return nil
}

func displayPropsEqMsg(c *cli.Context, bck cmn.Bck) {
	args := c.Args().Tail()
	if len(args) == 1 && !isJSON(args[0]) {
		fmt.Fprintf(c.App.Writer, "Bucket %q: property %q, nothing to do\n", bck.Cname(""), args[0])
		return
	}
	fmt.Fprintf(c.App.Writer, "Bucket %q already has the same values of props, nothing to do\n", bck.Cname(""))
}

func showDiff(c *cli.Context, currProps, newProps *cmn.BucketProps) {
	var (
		origKV = bckPropList(currProps, true)
		newKV  = bckPropList(newProps, true)
	)
	for idx, prop := range newKV {
		if origKV[idx].Value != prop.Value {
			fmt.Fprintf(c.App.Writer, "%q set to: %q (was: %q)\n", prop.Name, prop.Value, origKV[idx].Value)
		}
	}
}

func listAnyHandler(c *cli.Context) error {
	var (
		opts = cmn.ParseURIOpts{IsQuery: true}
		uri  = c.Args().First()
	)
	uri = preparseBckObjURI(uri)
	bck, objName, err := cmn.ParseBckObjectURI(uri, opts)
	if err != nil {
		return err
	}

	switch {
	case objName != "": // list archive OR show specific obj (HEAD(obj))
		if flagIsSet(c, listArchFlag) {
			return listArchHandler(c)
		}
		if _, err := headBucket(bck, true /* don't add */); err != nil {
			return err
		}
		return showObjProps(c, bck, objName)
	case bck.Name == "": // list buckets
		fltPresence := apc.FltPresent
		if flagIsSet(c, allObjsOrBcksFlag) {
			fltPresence = apc.FltExists
		}
		if flagIsSet(c, bckSummaryFlag) {
			if !apc.IsFltPresent(fltPresence) && bck.Provider != apc.AIS {
				warn := fmt.Sprintf("cannot _summarize_ non-present buckets and/or remote content - ignoring flag %s "+
					"(hint: use 'ais storage summary')\n", qflprn(allObjsOrBcksFlag))
				actionWarn(c, warn)
				fltPresence = apc.FltPresent
			}
		}
		return listBuckets(c, cmn.QueryBcks(bck), fltPresence)
	default: // list objects
		prefix := parseStrFlag(c, listObjPrefixFlag)
		listArch := flagIsSet(c, listArchFlag) // include archived content, if requested
		return listObjects(c, bck, prefix, listArch)
	}
}
