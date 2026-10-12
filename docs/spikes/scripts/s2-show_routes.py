import json, sys, os
SP = os.environ["SPIKES"]
d = json.load(sys.stdin)
print("app:", d["app"]["module"], "| fw:", d["framework"], "| build:", d["build"])
rh = d["app"]["root_hint"]
print("root_hint:", rh["symbol"], rh["file"].replace(SP, "$SPIKES"), rh["line"])
def short(h):
    return "%s @ %s:%d" % (h["symbol"], h["file"].replace(SP, "$SPIKES"), h["line"])
for r in d["routes"]:
    h = r["handler"]
    print("%-4s %-18s [%-9s] %s" % (r["method"], r["path"], h["origin"], short(h)))
    for m in r.get("middleware", []):
        print("       mw [%-9s] %s" % (m["origin"], short(m)))
