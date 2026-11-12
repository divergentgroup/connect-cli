package auth

import (
	_ "embed"
	"html"
	"os/user"
	"strings"
)

//go:embed divergent.svg
var divergentLogo string

// logoHTML is the Connect logo as coloured braille art; regenerate with scripts/logoart.
//
//go:embed logo.html
var logoHTML string

// closePage renders what the browser shows once the loopback callback has been accepted:
// a terminal that types out the Connect logo and the sign-in confirmation.
func closePage() string {
	return strings.NewReplacer("{divergent}", divergentLogo, "{logo}", strings.TrimRight(logoHTML, "\n"), "{user}", html.EscapeString(promptUser())).Replace(pageTemplate)
}

// promptUser is the local account name, lowercased and clipped, as a shell prompt would show it.
func promptUser() string {
	name := "you"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndexAny(name, "\\/"); i >= 0 { // DOMAIN\\user on Windows
		name = name[i+1:]
	}
	if len(name) > 15 {
		name = name[:15]
	}
	return name
}

const pageTemplate = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Connect</title>
<style>
html,body{height:100%;margin:0}
body{background:#0b0b0c;color:#e8e8e6;font:15px/1.35 ui-monospace,SFMono-Regular,Menlo,Consolas,"Liberation Mono",monospace;display:flex;align-items:center;justify-content:center;padding:2rem;box-sizing:border-box}
.wrap{display:grid}
.wrap>pre{grid-area:1/1;margin:0;white-space:pre;font:inherit}
#g{visibility:hidden}
.p{color:#17e5e5}.d{color:#7a7a78}
.lg{line-height:1.05;display:inline-block}
.lg b{font-weight:400}.lg .w{color:#f4f4ed}.lg .c{color:#17e5e5}.lg .b{color:#0d4fa3}.lg .o{color:#0b9bd0}
.dv{position:fixed;right:2rem;bottom:2rem;width:150px;color:#e8e8e6;opacity:.32}
.dv svg{display:block;width:100%;height:auto}
@media (max-width:900px){body{font-size:10px}.dv{width:100px}}
</style></head>
<body><div class="wrap"><pre id="g"></pre><pre id="t"></pre></div><div class="dv">{divergent}</div>
<script type="text/plain" id="logo">{logo}</script>
<script>
(function(){
var logo=document.getElementById('logo').textContent.split('\n');
var t=document.getElementById('t'),g=document.getElementById('g');
var prompt='<span class="p">{user}@connect</span> <span class="d">~</span> $ ';
var msg1="All signed in. You're free to close this tab and return",msg2='to the terminal where you can be more comfortable.';
function esc(s){return s.replace(/&/g,'&amp;').replace(/</g,'&lt;')}
function lg(n){return '<span class="lg">'+logo.slice(0,n).join('\n')+'</span>'}
function screen(cmd,n,a,b){return prompt+esc(cmd)+(n<0?'':'\n\n'+lg(n)+(n<logo.length?'':'\n\n'+esc(a||'')+(b===undefined?'':'\n'+esc(b))))}
g.innerHTML=screen('connect auth login',logo.length,msg1,msg2);
var fast=matchMedia('(prefers-reduced-motion: reduce)').matches;
var steps=[];
var cmd='connect auth login';
for(var i=1;i<=cmd.length;i++)steps.push({h:screen(cmd.slice(0,i),-1),d:45+Math.random()*50});
steps.push({h:screen(cmd,0),d:350});
for(var i=1;i<=logo.length;i++)steps.push({h:screen(cmd,i),d:60});
steps.push({h:screen(cmd,logo.length,'',undefined),d:300});
for(var i=1;i<=msg1.length;i++)steps.push({h:screen(cmd,logo.length,msg1.slice(0,i)),d:18+Math.random()*30});
for(var i=1;i<=msg2.length;i++)steps.push({h:screen(cmd,logo.length,msg1,msg2.slice(0,i)),d:18+Math.random()*30});
if(fast){t.innerHTML=g.innerHTML;return}
var k=0;(function run(){if(k>=steps.length)return;var s=steps[k++];setTimeout(function(){t.innerHTML=s.h;run()},s.d)})();
})();
</script></body></html>`
