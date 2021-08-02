/*
* Copyright (C) 2020 THL A29 Limited, a Tencent company.  All rights reserved.
* This source code is licensed under the Apache License Version 2.0.
*/

package cmds

import "github.com/spf13/cobra"

func init() {
	rootCmd.AddCommand(InitCmd)
}

var InitCmd = &cobra.Command{
	Use:   "init",
	Short: "Init demo or dep component",
	Long:  `Init demo or dep component`,
}
