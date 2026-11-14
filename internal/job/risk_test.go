package job

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRiskRoundTrip(t *testing.T) {
	var old Job
	require.NoError(t, json.Unmarshal([]byte(`{"id":"j1","status":"completed"}`), &old), "a job stored before risk existed")
	require.Nil(t, old.Risk)
	b, err := json.Marshal(old)
	require.NoError(t, err)
	require.Contains(t, string(b), `"risk":null`, "clients get risk: null")

	var null Job
	require.NoError(t, json.Unmarshal([]byte(`{"id":"j1","risk":null}`), &null))
	require.Nil(t, null.Risk)

	raw := `{"version":1,"score":0.12345678901234568,"x":[1,2]}`
	var with Job
	require.NoError(t, json.Unmarshal([]byte(`{"id":"j1","risk":`+raw+`}`), &with))
	require.Equal(t, raw, string(with.Risk), "verbatim")
	c := with.Clone()
	require.Equal(t, raw, string(c.Risk), "Clone keeps it")
}
