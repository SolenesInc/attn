import os,re,sys
TEXT=re.compile(r"(Said|String|\bstr\b)")
rows=[]
for root,_,files in os.walk('.'):
    for f in sorted(files):
        if not f.endswith('.rs') or 'gallery' in f or f in ('playground.rs',): continue
        p=os.path.join(root,f)[2:]
        s=open(os.path.join(root,f)).read()
        owner=None
        i=0
        lines=s.split('\n')
        src=s
        for m in re.finditer(r"^(impl(?:<[^>]*>)?\s+(?:[\w:<>, ]+\s+for\s+)?(\w+)[^{]*\{)|^\s*pub fn (\w+)\s*(<[^(]*>)?\(", s, flags=re.M):
            if m.group(2): owner=m.group(2); continue
            if m.start() and s[:m.start()].rfind('\n}')>s[:m.start()].rfind('impl'): owner=None
            name=m.group(3)
            # params until matching paren
            j=m.end(); depth=1; k=j
            while depth:
                c=s[k]; depth+= (c=='(') - (c==')'); k+=1
            params=s[j:k-1]
            # split top-level commas
            parts=[];d=0;cur=''
            for c in params:
                if c in '<([': d+=1
                if c in '>)]': d-=1
                if c==',' and d==0: parts.append(cur); cur=''
                else: cur+=c
            parts.append(cur)
            for prm in parts:
                prm=prm.strip()
                if ':' not in prm or prm.startswith('self') or prm.startswith('mut self') or prm.startswith('&'): 
                    if not (':' in prm and not prm.startswith('&self')): continue
                pn,ty=prm.split(':',1); pn=pn.replace('mut ','').strip(); ty=' '.join(ty.split())
                if pn in ('key','id') or pn.endswith('_key') or pn.endswith('_id'): continue
                if not TEXT.search(ty) or 'Fn(' in ty or 'Tokens' in ty: continue
                kind='said' if ('Said' in ty or "'static str" in ty) else 'plain'
                mod=p[:-3].replace('/','::').replace('::mod','')
                rows.append((f"{mod}::{(owner+'::') if owner else ''}{name}({pn})",kind,ty))
for r in rows: print(f"{r[1]:5} {r[0]:60} {r[2]}")
print(len(rows), file=sys.stderr)
