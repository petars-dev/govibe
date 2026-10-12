import json,sys,re,html
base=sys.argv[1]
for sc in ['all','app','framework']:
    print('=====',sc)
    st=json.load(open(f'{base}/{sc}/status.json'))
    f=st['freshness']
    print('status: quads',f['quads'],'added',f['added'],'findings',st['findings'])
    print('  layer quads',{l['name']:l.get('quads') for l in f['layers']}, 'sum',sum(l.get('quads') or 0 for l in f['layers']))
    for p in ['architecture','request_flow','db_er','docs_coverage','config_usage','findings']:
        v=json.load(open(f'{base}/{sc}/view_{p}.json'))
        ks={k['kind']:k['count'] for k in v.get('kinds',[])} if v.get('kinds') else {}
        print(f"  view {p:14s} nodes={len(v['nodes']):4d} edges={len(v['edges']):4d} notes={v.get('notes')} kinds={ks}")
    d=json.load(open(f'{base}/{sc}/mcp_graph_findings.txt'))
    print('  graph_findings', d['total'], d['by_rule'])
    for fi in d['findings']: print('     ',fi['severity'],fi['rule'],fi.get('about'))
