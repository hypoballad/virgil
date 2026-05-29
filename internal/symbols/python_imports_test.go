package symbols

import "testing"

func TestExtractPythonImportsCoversCommonPatterns(t *testing.T) {
	src := []byte(`
import os
import sys
import os.path
import xml.etree.ElementTree
import numpy as np
import pandas as pd
from typing import List
from collections import defaultdict
from typing import List, Dict, Optional
from typing import (
    Set,
    Tuple,
)
from numpy import ndarray as NDArray
from typing import List as ListType
from . import helper
from .utils import foo
from ..models import Bar
from ...config import settings
from typing import *

def lazy_load():
    import heavy_module
    return heavy_module.do_something()

try:
    import json
except ImportError:
    import simplejson as json
`)

	imports, err := NewExtractor().ExtractImportsFromSource(src, "python")
	if err != nil {
		t.Fatalf("ExtractImportsFromSource() error = %v", err)
	}

	assertImport(t, imports, Import{Kind: "import", Module: "os", Scope: "module"})
	assertImport(t, imports, Import{Kind: "import", Module: "os.path", Scope: "module"})
	assertImport(t, imports, Import{Kind: "import", Module: "xml.etree.ElementTree", Scope: "module"})
	assertImport(t, imports, Import{Kind: "import", Module: "numpy", Alias: "np", Scope: "module"})
	assertImport(t, imports, Import{Kind: "from_import", Module: "typing", ImportedName: "List", Scope: "module"})
	assertImport(t, imports, Import{Kind: "from_import", Module: "typing", ImportedName: "Dict", Scope: "module"})
	assertImport(t, imports, Import{Kind: "from_import", Module: "typing", ImportedName: "Optional", Scope: "module"})
	assertImport(t, imports, Import{Kind: "from_import", Module: "typing", ImportedName: "Set", Scope: "module"})
	assertImport(t, imports, Import{Kind: "from_import", Module: "numpy", ImportedName: "ndarray", Alias: "NDArray", Scope: "module"})
	assertImport(t, imports, Import{Kind: "from_import", Module: ".", ImportedName: "helper", IsRelative: true, RelativeLevel: 1, Scope: "module"})
	assertImport(t, imports, Import{Kind: "from_import", Module: ".utils", ImportedName: "foo", IsRelative: true, RelativeLevel: 1, Scope: "module"})
	assertImport(t, imports, Import{Kind: "from_import", Module: "..models", ImportedName: "Bar", IsRelative: true, RelativeLevel: 2, Scope: "module"})
	assertImport(t, imports, Import{Kind: "from_import", Module: "...config", ImportedName: "settings", IsRelative: true, RelativeLevel: 3, Scope: "module"})
	assertImport(t, imports, Import{Kind: "from_import", Module: "typing", ImportedName: "*", IsWildcard: true, Scope: "module"})
	assertImport(t, imports, Import{Kind: "import", Module: "heavy_module", Scope: "function"})
	assertImport(t, imports, Import{Kind: "import", Module: "json", Scope: "conditional"})
	assertImport(t, imports, Import{Kind: "import", Module: "simplejson", Alias: "json", Scope: "conditional"})
}

func assertImport(t *testing.T, imports []Import, want Import) {
	t.Helper()
	for _, got := range imports {
		if got.Kind == want.Kind &&
			got.Module == want.Module &&
			got.ImportedName == want.ImportedName &&
			got.Alias == want.Alias &&
			got.IsRelative == want.IsRelative &&
			got.RelativeLevel == want.RelativeLevel &&
			got.IsWildcard == want.IsWildcard &&
			got.Scope == want.Scope {
			return
		}
	}
	t.Fatalf("missing import %+v in %+v", want, imports)
}
