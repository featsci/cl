package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"cli/internal/state"
)

// handleCheckState loads the state and outputs it
func handleCheckState(backend *state.Backend, jsonOutput bool) {
	if backend == nil {
		fmt.Println("[ERROR] S3 Backend not configured.")
		return
	}

	fmt.Println("[REFRESH] Loading state from S3...")
	st, err := backend.LoadState()
	if err != nil {
		fmt.Printf("[ERROR] State loading error: %v\n", err)
		return
	}

	if st == nil {
		fmt.Println("[ENVELOPE] State file not found (cluster not created?).")
		return
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(st)
		return
	}

	fmt.Printf("\n[CALENDAR] Last Updated: %s\n", st.LastUpdated.Format(time.RFC1123))
	fmt.Printf("[PERSON] SSH User:     %s\n", st.SSHUser)
	fmt.Println("")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	// --- MODIFICATION: Added ADDRESS ID column ---
	fmt.Fprintln(w, "NAME\tIP ADDRESS\tROLE\tSERVER ID\tADDRESS ID")
	fmt.Fprintln(w, "----\t----------\t----\t---------\t----------")

	for _, node := range st.Nodes {
		// --- MODIFICATION: Outputting new field node.AddressID ---
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", node.Name, node.IP, node.Role, node.ID, node.AddressID)
	}
	w.Flush()
	fmt.Println("")
}
