package main

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

func TestContextReplacementFlagsCaptureCompleteSetOrClear(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		want    []string
		wantSet bool
		wantErr bool
	}{
		{name: "replacement", args: []string{"--context", "one", "--context", "two"}, want: []string{"one", "two"}, wantSet: true},
		{name: "contextless", args: []string{"--contextless"}, wantSet: true},
		{name: "mutually exclusive", args: []string{"--context", "one", "--contextless"}, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().StringArray("context", nil, "")
			cmd.Flags().Bool("contextless", false, "")
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			got, set, err := contextReplacementFlags(cmd)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && (!set == tt.wantSet || !reflect.DeepEqual(got, tt.want)) {
				t.Fatalf("flags = (%v, %v), want (%v, %v)", got, set, tt.want, tt.wantSet)
			}
		})
	}
}
