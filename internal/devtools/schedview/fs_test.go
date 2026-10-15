package schedview

import "testing/fstest"

// mapFS is what an app's embedded env.yml looks like from the outside.
func mapFS(yml string) fstest.MapFS { return fstest.MapFS{"env.yml": {Data: []byte(yml)}} }
