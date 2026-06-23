package cli

import "github.com/dbmurphy/elasticsearch-filesystem/internal/escore"

func writePolicyName(p escore.WritePolicy) string { return p.String() }
