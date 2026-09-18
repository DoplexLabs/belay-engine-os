package numbat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type InventoryRow struct {
	Agent                string                     `json:"agent"`
	Present              bool                       `json:"present"`
	Detected             bool                       `json:"detected"`
	AtRest               string                     `json:"at_rest"`
	Hook                 string                     `json:"hook"`
	Wired                string                     `json:"wired"`
	SetupHint            string                     `json:"setup_hint"`
	AtRestScanIncomplete bool                       `json:"at_rest_scan_incomplete"`
	Raw                  map[string]json.RawMessage `json:"raw,omitempty"`
}

type Inventory struct {
	Rows          []InventoryRow         `json:"rows"`
	LaunchTargets map[Agent]InventoryRow `json:"launch_targets"`
	UnknownRows   []InventoryRow         `json:"other_present_agents"`
}

func (c *Client) Discover(ctx context.Context) (Inventory, CommandResult, error) {
	result, err := c.run(ctx, "agents", "agents", "--format", "json")
	if err != nil {
		return Inventory{}, result, err
	}
	inventory, err := parseInventory([]byte(result.Stdout))
	if err != nil {
		return Inventory{}, result, fmt.Errorf("parse numbat agent inventory: %w", err)
	}
	return inventory, result, nil
}

func parseInventory(data []byte) (Inventory, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var rawRows []json.RawMessage
	if err := decoder.Decode(&rawRows); err != nil {
		return Inventory{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Inventory{}, err
	}

	inventory := Inventory{
		Rows:          make([]InventoryRow, 0, len(rawRows)),
		LaunchTargets: make(map[Agent]InventoryRow),
	}
	for index, raw := range rawRows {
		row, err := parseInventoryRow(raw)
		if err != nil {
			return Inventory{}, fmt.Errorf("row %d: %w", index, err)
		}
		inventory.Rows = append(inventory.Rows, row)
		agent, known := parseAgent(row.Agent)
		if !known {
			inventory.UnknownRows = append(inventory.UnknownRows, row)
			continue
		}
		if _, duplicate := inventory.LaunchTargets[agent]; duplicate {
			return Inventory{}, fmt.Errorf("duplicate %q inventory row", row.Agent)
		}
		inventory.LaunchTargets[agent] = row
	}
	return inventory, nil
}

func parseInventoryRow(raw json.RawMessage) (InventoryRow, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return InventoryRow{}, err
	}
	if fields == nil {
		return InventoryRow{}, errors.New("inventory row must be an object")
	}
	var wire struct {
		Agent                string `json:"agent"`
		Present              bool   `json:"present"`
		Detected             bool   `json:"detected"`
		AtRest               string `json:"at_rest"`
		Hook                 string `json:"hook"`
		Wired                string `json:"wired"`
		SetupHint            string `json:"setup_hint"`
		AtRestScanIncomplete bool   `json:"at_rest_scan_incomplete"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return InventoryRow{}, err
	}
	wire.Agent = strings.TrimSpace(wire.Agent)
	if wire.Agent == "" {
		return InventoryRow{}, errors.New("agent is required")
	}
	return InventoryRow{
		Agent:                wire.Agent,
		Present:              wire.Present,
		Detected:             wire.Detected,
		AtRest:               wire.AtRest,
		Hook:                 wire.Hook,
		Wired:                wire.Wired,
		SetupHint:            wire.SetupHint,
		AtRestScanIncomplete: wire.AtRestScanIncomplete,
		Raw:                  fields,
	}, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected data after inventory")
	}
	return err
}
