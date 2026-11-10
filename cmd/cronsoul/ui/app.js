const $=s=>document.querySelector(s), app=$('#app');
const state={page:'endpoints',me:null,jobs:[],runs:[],channels:[],alerts:[],outbox:[],settings:{},users:[],allowlist:[],audit:[],selected:null,error:'',sortDir:'asc',auditQ:'',auditTotal:null,auditOffset:0,allowQ:'',upcoming:null,upcomingAt:0,stats:null,epFilter:'all',epPage:1,timeline:null};
const icons={timeline:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 6h16M4 12h10M4 18h13"/><circle cx="19" cy="12" r="2"/></svg>',endpoints:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 8h10M7 12h4M7 16h7"/></svg>',runs:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 5h16v14H4zM8 9h8M8 13h5"/></svg>',alerts:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4"/></svg>',channels:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 6h16v12H4zM4 7l8 6 8-6"/></svg>',settings:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 3v3m0 12v3M3 12h3m12 0h3"/><circle cx="12" cy="12" r="4"/></svg>',users:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="9" cy="8" r="3"/><path d="M3 20c0-3 2-5 6-5s6 2 6 5M17 15c2.7.4 4 2 4 5"/></svg>',allowlist:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 3 4 6v5c0 5 3.4 8.3 8 10 4.6-1.7 8-5 8-10V6z"/><path d="m8 12 2.5 2.5L16 9"/></svg>',audit:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M5 4h14v16H5zM8 8h8M8 12h8M8 16h5"/></svg>'};
function kpi(label,value,hint,tone){return `<div class="card kpi ${tone||''}"><div class="kpi-label"><span>${label}</span></div><div class="kpi-value">${value}</div><div class="kpi-hint">${hint}</div></div>`}
async function api(path,opts={}){const r=await fetch('/api/v1'+path,{credentials:'same-origin',headers:{'Content-Type':'application/json',...(opts.headers||{})},...opts});let d=null;try{d=await r.json()}catch{}if(!r.ok)throw Error(d?.error||d||r.statusText);return d}
function esc(v){return String(v??'').replace(/[&<>"']/g,x=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[x]))}
const TOAST_DURATION_KEY='cronsoul.toastDuration';
function toastDuration(){try{const n=Number(localStorage.getItem(TOAST_DURATION_KEY));return [2000,3000,4000,6000].includes(n)?n:3000}catch{return 3000}}
function toastLimit(){return window.matchMedia&&window.matchMedia('(max-width: 680px)').matches?1:3}
function closeToast(x,immediate=false){if(!x)return;if(x.__toastTimer)clearTimeout(x.__toastTimer);if(immediate){x.remove();return}if(x.classList.contains('is-closing'))return;x.classList.add('is-closing');setTimeout(()=>x.remove(),220)}
function notify(msg,opts={}){let host=document.querySelector('.toast-stack');if(!host){host=document.createElement('div');host.className='toast-stack';host.setAttribute('role','region');host.setAttribute('aria-label','Notifications');host.setAttribute('aria-live','polite');document.body.append(host)}const x=document.createElement('div');x.className='toast';const text=document.createElement('span');text.className='toast-message';text.textContent=String(msg??'');const close=document.createElement('button');close.type='button';close.className='toast-close';close.setAttribute('aria-label','Dismiss notification');close.textContent='×';x.append(text,close);host.append(x);const ms=Number.isFinite(opts.duration)?Math.max(1000,Math.min(15000,opts.duration)):toastDuration();x.style.setProperty('--toast-duration',ms+'ms');requestAnimationFrame(()=>x.classList.add('is-visible'));x.__toastTimer=setTimeout(()=>closeToast(x),ms);close.onclick=()=>closeToast(x);const active=[...host.children].filter(n=>!n.classList.contains('is-closing'));const max=toastLimit();if(active.length>max)active.slice(0,active.length-max).forEach(n=>closeToast(n,true))}
function persistToastDuration(value){const n=Number(value);if(![2000,3000,4000,6000].includes(n))return;try{localStorage.setItem(TOAST_DURATION_KEY,String(n))}catch{}const hint=document.getElementById('toast-duration-current');if(hint)hint.textContent=(n/1000)+' seconds'}
function runtimeSettingRows(settings,env){const defs=[['scheduler.enabled','Automatic scheduler','Starts cron jobs in the background; manual runs still work when disabled.'],['session_ttl_hours','Session lifetime','How long an authenticated browser session stays valid before re-login.'],['max_response_bytes','Response body limit','Bytes of response body kept per run. Larger bodies are truncated; the hard cap is 1 MiB.'],['allow_private_egress','Private network egress','Whether explicitly allowed private targets may be called. Protected metadata ranges remain blocked.'],['allow_insecure_targets','Temporary HTTP mode','Time-limited test exception for non-HTTPS targets; disabled in production.'],['follow_redirects','Redirect following','CronSoul refuses redirects to prevent destination changes.']];return `<section class="runtime-card card"><header class="runtime-head"><div><h2>Runtime configuration</h2><p class="muted">Read-only values from this instance. Change them through the documented environment or Settings controls.</p></div><span class="pill neutral">${esc(env||'dev')}</span></header><div class="runtime-list">${defs.filter(([k])=>Object.hasOwn(settings,k)).map(([k,label,help])=>{const v=settings[k];return `<div class="runtime-row"><div class="runtime-copy"><strong>${esc(label)}</strong><small>${esc(help)}</small></div><span class="runtime-value ${v===true?'is-on':v===false?'is-off':''}">${esc(settingDisplay(k,v))}</span></div>`}).join('')}</div><p class="runtime-env-note"><b>Environment mode</b><span><code>${esc(env||'dev')}</code> is a runtime configuration mode, not an environment selector. <code>dev</code> relaxes production startup checks and permits TLS off for trusted local testing. Use <code>production</code> behind HTTPS with a persistent database and encryption key before remote use.</span></p></section>`}
function settingDisplay(k,v){
  if(typeof v==='boolean')return v?'Enabled':'Disabled';
  if(k==='max_response_bytes'){const n=Number(v);if(Number.isFinite(n))return n.toLocaleString('en-US')+' bytes · '+(n/1024).toFixed(0)+' KiB'}
  if(k==='session_ttl_hours'){const n=Number(v);if(Number.isFinite(n))return n+' hours'}
  return String(v)
}
const PW_EYE='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path class="eye-open" d="M2.8 12S6.2 5.6 12 5.6 21.2 12 21.2 12 17.8 18.4 12 18.4 2.8 12 2.8 12Z"/><circle class="eye-open" cx="12" cy="12" r="2.7"/><path class="eye-off" d="m4 4 16 16M9.6 9.7a2.7 2.7 0 0 0 3.9 3.8"/></svg>';
function login(){app.innerHTML=`<main class="login"><div class="login-shell">
<aside class="login-side">
  <div class="login-brand"><img src="/ui/cronsoul-mark.svg" alt=""><span>CronSoul<span class="brand-dot">.</span></span></div>
  <div class="login-side-body">
    <p class="login-kicker">Operational console</p>
    <h1>Every schedule,<br>one place to watch.</h1>
    <p>Sign in to see each endpoint fire on time, find exactly what failed, and retry or replay a run from its history.</p>
    <ul class="login-facts">
      <li><i aria-hidden="true"></i>Live runs plus a 24-hour timeline per endpoint</li>
      <li><i aria-hidden="true"></i>Retry, replay and inspect failures in place</li>
      <li><i aria-hidden="true"></i>Alerts when an endpoint misbehaves silently</li>
    </ul>
  </div>
  <div class="login-side-foot"><span>ENV</span><b id="login-env">${esc(state['env']||'standalone')}</b><span>· protected instance</span></div>
</aside>
<div class="login-panel">
  <form class="login-card" id="login" novalidate>
    <h2>Sign in</h2>
    <p class="muted">Use your operator account to continue.</p>
    <div class="field"><label for="login-user">Username</label><input id="login-user" name="username" autocomplete="username" required autofocus></div>
    <div class="field"><label for="login-pass">Password</label><span class="pw"><input id="login-pass" name="password" type="password" autocomplete="current-password" required><button type="button" class="pw-toggle" data-pw="login-pass" aria-label="Show password" aria-pressed="false">${PW_EYE}</button></span></div>
    <div id="err" class="error" role="alert" aria-live="polite"></div>
    <button class="primary" type="submit"><span class="spin" aria-hidden="true"></span><span class="btn-label">Sign in</span></button>
    <p class="login-note">Locked out? Ask an admin to reset your password from Users.</p>
  </form>
</div>
</div></main>`;wireLoginForm('login',async f=>{state.me=await api('/auth/login',{method:'POST',body:JSON.stringify(f)});if(state.me.must_change_password)return changePassword();render()},'Signing in…','Sign in')}
function changePassword(){app.innerHTML=`<main class="login"><div class="login-shell">
<aside class="login-side">
  <div class="login-brand"><img src="/ui/cronsoul-mark.svg" alt=""><span>CronSoul<span class="brand-dot">.</span></span></div>
  <div class="login-side-body">
    <p class="login-kicker">Account security</p>
    <h1>Set a new<br>password.</h1>
    <p>Your administrator requires a password change before you can use the console.</p>
    <ul class="login-facts">
      <li><i aria-hidden="true"></i>Use at least 10 characters</li>
      <li><i aria-hidden="true"></i>Keep it different from your current password</li>
      <li><i aria-hidden="true"></i>Your session continues right after the change</li>
    </ul>
  </div>
  <div class="login-side-foot"><span>ENV</span><b id="login-env">${esc(state['env']||'standalone')}</b><span>· protected instance</span></div>
</aside>
<div class="login-panel">
  <form class="login-card" id="change" novalidate>
    <h2>Update password</h2>
    <p class="muted">Required before you can continue.</p>
    <div class="field"><label for="change-new">New password</label><span class="pw"><input id="change-new" name="new_password" type="password" minlength="10" autocomplete="new-password" required autofocus><button type="button" class="pw-toggle" data-pw="change-new" aria-label="Show password" aria-pressed="false">${PW_EYE}</button></span></div>
    <div id="err" class="error" role="alert" aria-live="polite"></div>
    <button class="primary" type="submit"><span class="spin" aria-hidden="true"></span><span class="btn-label">Update password</span></button>
    <p class="login-note">This changes the password for your operator account only.</p>
  </form>
</div>
</div></main>`;wireLoginForm('change',async f=>{await api('/auth/change-password',{method:'POST',body:JSON.stringify(f)});state.me.must_change_password=false;render()},'Updating…','Update password')}
function wireLoginForm(id,onSuccess,busyText,btnLabel){
  const form=document.getElementById(id);
  const err=document.getElementById('err');
  const submit=form.querySelector('button[type="submit"]');
  const label=submit.querySelector('.btn-label');
  const setBusy=on=>{submit.disabled=on;submit.classList.toggle('is-busy',on);if(on)submit.setAttribute('aria-busy','true');else submit.removeAttribute('aria-busy');label.textContent=on?busyText:btnLabel;form.querySelectorAll('input').forEach(i=>i.disabled=on)};
  form.querySelectorAll('.pw-toggle').forEach(b=>b.addEventListener('click',()=>{const i=document.getElementById(b.dataset.pw);const show=i.type==='password';i.type=show?'text':'password';b.classList.toggle('is-on',show);b.setAttribute('aria-pressed',String(show));b.setAttribute('aria-label',show?'Hide password':'Show password')}));
  form.querySelectorAll('input').forEach(i=>i.addEventListener('input',()=>{err.textContent=''}));
  form.addEventListener('submit',async e=>{
    e.preventDefault();
    err.textContent='';
    const data=Object.fromEntries(new FormData(form));
    for(const [name,value] of Object.entries(data)){
      const el=form.elements[name];
      if(!String(value||'').trim()){err.textContent='Enter '+name.replaceAll('_',' ')+'.';el?.focus();return}
      if(el?.minLength&&String(value).length<el.minLength){err.textContent=(name==='new_password'?'New password':name.replaceAll('_',' '))+' must be at least '+el.minLength+' characters.';el.focus();return}
    }
    setBusy(true);
    try{await onSuccess(data)}catch(x){err.textContent=x.message;form.querySelector('input')?.focus()}finally{setBusy(false)}
  });
}
function render(){app.innerHTML=`<div class="shell"><aside class="side"><div class="brand"><img src="/ui/cronsoul-mark.svg" alt="CronSoul"><span>CronSoul<span class="brand-dot">.</span></span></div><nav class="nav">${[['endpoints','Endpoints'],['runs','Runs'],['timeline','Timeline'],['alerts','Alerts'],['channels','Channels'],['settings','Settings'],['users','Users'],['allowlist','Allowlist'],['audit','Audit']].filter(([k])=>['users','audit'].includes(k)?['admin','superadmin'].includes(state.me?.role):k==='allowlist'?state.me?.role==='superadmin':true).map(([k,v])=>`<button class="${state.page===k?'active':''}" title="${v}" data-page="${k}">${icons[k]} <span>${v}</span></button>`).join('')}</nav></aside><main class="main"><div class="top"><div><h1>${title()}</h1><div class="muted">${state.me?.username||''} · ${state.me?.role||''}</div></div>${state.page==='endpoints'?'<button class="primary" id="new">＋ Endpoint baru</button>':''}</div><section id="content">Loading…</section></main></div>`;document.querySelectorAll('[data-page]').forEach(b=>b.onclick=()=>{state.page=b.dataset.page;render()});if($('#new'))$('#new').onclick=()=>endpointForm();governanceAction();loadPage()}
function title(){return {endpoints:'Endpoints',runs:'Run history',timeline:'Timeline',alerts:'Alerts',channels:'Notification channels',settings:'Settings',users:'Users',allowlist:'Egress allowlist',audit:'Audit log'}[state.page]}
async function loadPage(){try{if(state.page==='endpoints'){const [d,st]=await Promise.all([api('/jobs/'),api('/stats')]);state.jobs=d.data||[];state.stats=st}else if(state.page==='timeline'){const [tl,js]=await Promise.all([api('/timeline?range='+(state.tlRange||'today')),api('/jobs/')]);state.timeline=tl;state.jobs=js.data||[]}else if(state.page==='runs'){state.runs=(await api('/runs')).data;try{state.upcoming=await api('/jobs/upcoming?minutes=60');state.upcomingAt=Date.now()}catch{state.upcoming=null}}else if(state.page==='channels')state.channels=await api('/channels');else if(state.page==='alerts'){const [a,o]=await Promise.all([api('/alerts'),api('/outbox')]);state.alerts=a.data||[];state.outbox=o.data||[]}else if(state.page==='settings'){const [st,sec]=await Promise.all([api('/settings'),api('/security')]);state.settings=st.data||{};state.security=sec||{}}else if(state.page==='users')state.users=(await api('/users')).data||[];else if(state.page==='allowlist')state.allowlist=(await api('/allowlist')).data||[];else if(state.page==='audit'){const q=new URLSearchParams({actor:state.auditActor||'',action:state.auditAction||'',target_type:state.auditType||'',q:state.auditQ||'',limit:'100',offset:String(state.auditOffset||0)});const d=await api('/audit?'+q);state.audit=d.data||[];state.auditTotal=d.total??state.audit.length}draw()}catch(e){$('#content').innerHTML=`<div class="card error">${esc(e.message)}</div>`}}
function draw(){const c=$('#content');if(state.page==='endpoints')c.innerHTML=`<div class="card"><table class="table"><thead><tr><th class="sortable" data-sort="name" tabindex="0" role="button">Endpoint<span class="sort-ind">${state.sortDir==='desc'?'↓':'↑'}</span></th><th>Service</th><th>Schedule</th><th>Status</th><th></th></tr></thead><tbody>${state.jobs.map(j=>`<tr><td><strong>${esc(j.name)}</strong><br><span class="muted">${esc(j.key)} · ${esc(j.method)} ${esc(j.url)}</span></td><td>${esc(j.service)}</td><td>${esc(j.cron)}<br><span class="muted">${esc(j.timezone)}</span></td><td class="${j.enabled?'success':'warning'}"><span class="pill"><i class="dot"></i>${j.enabled?'Enabled':'Paused'}</span></td><td><button data-play="${j.id}">▶ Play</button></td></tr>`).join('')||'<tr><td colspan="5" class="muted">No endpoints yet.</td></tr>'}</tbody></table></div>`;else if(state.page==='runs')c.innerHTML=`<div class="card"><table class="table"><thead><tr><th>Run</th><th>Trigger</th><th>Status</th><th>Created</th></tr></thead><tbody>${state.runs.map(r=>`<tr data-run="${r.id}"><td>${esc(r.id.slice(0,8))}<br><span class="muted">${esc(r.job_id)}</span></td><td>${esc(r.trigger)}</td><td><span class="pill ${statusClass(r.status)}"><i></i>${esc(statusLabel(r.status))}</span></td><td>${esc(r.created_at)}</td></tr>`).join('')||'<tr><td colspan="4" class="muted">No runs yet.</td></tr>'}</tbody></table></div>`;else if(state.page==='channels'){c.innerHTML=channelView()}else if(state.page==='alerts'){c.innerHTML=alertsView()}else if(state.page==='users')c.innerHTML=`<div class="card"><table class="table"><thead><tr><th>User</th><th>Role</th><th>Status</th><th>Actions</th></tr></thead><tbody>${state.users.map(u=>`<tr><td>${esc(u.username)}<br><span class="muted">${esc(u.email||'')}</span></td><td>${esc(u.role)}</td><td>${u.disabled?'Disabled':'Active'}</td><td>${u.id===state.me?.id?'Current user':`<button data-user-action="reset-password" data-id="${u.id}">Reset</button> <button data-user-action="${u.disabled?'enable':'disable'}" data-id="${u.id}">${u.disabled?'Enable':'Disable'}</button> <button data-user-action="delete" data-id="${u.id}">Delete</button>`}</td></tr>`).join('')}</tbody></table></div>`;else if(state.page==='allowlist')c.innerHTML=`<div class="card">${state.allowlist.map(x=>`<p>${esc(x.pattern)} <span class="muted">${esc(x.note||'')}</span> <button data-allow-delete="${x.id}">Delete</button></p>`).join('')||emptyBlock('Allowlist is empty','No outbound destination is permitted yet, so every job will be refused at run time. Add the hosts your jobs call.','')}</div>`;else if(state.page==='audit')c.innerHTML=`<div class="card"><form id="audit-filter"><input name="actor" placeholder="Actor" value="${esc(state.auditActor||'')}"><input name="action" placeholder="Action" value="${esc(state.auditAction||'')}"><input name="target_type" placeholder="Target type" value="${esc(state.auditType||'')}"><button>Filter</button></form>${state.audit.map(x=>`<p>${esc(x.at)} · ${esc(x.actor_name)} · ${esc(x.action)} · ${esc(x.target_type||'')} · ${esc(x.target_id||'')}</p>`).join('')||emptyBlock('Nothing recorded yet','Governance changes such as users, allowlist, scheduler and security mode appear here with the actor and the before/after state.','')}<button id="audit-next">Next</button></div>`;else if(state.page==='settings'){const sched=state.settings['scheduler.enabled']===true;const sec=state.security||{};const env=state.env||'dev';const toast=toastDuration();c.innerHTML=`<div class="runtime-overview"><section class="card runtime-mode"><div class="runtime-card-head"><div><span class="runtime-eyebrow">INSTANCE MODE</span><h2>${esc(env)}</h2></div><span class="pill ${env==='dev'?'warning':'success'}"><i class="dot"></i>${env==='dev'?'Development':'Production'}</span></div><p class="muted">Read-only. This shows how the server process was started; it is not a switch inside this console.</p><div class="runtime-mode-note"><strong>${env==='dev'?'What dev means':'What production means'}</strong><span>${env==='dev'?'For trusted local testing: TLS may be off and relaxed startup defaults apply. Do not expose this instance directly to the internet.':'Every production guard is enforced: HTTPS base URL, file-backed database, stable encryption key, and TLS via proxy or certificate.'}</span></div><div class="runtime-mode-note runtime-howto"><strong>How to configure it</strong><span>Set it in the environment of the CronSoul process (compose file, <code>docker run -e</code>, or systemd unit), then restart the service. The console only reports the result.</span><ul class="runtime-env-list"><li><code>CRONSOUL_ENV</code><span>${env==='dev'?'set to production':'set to dev'}</span></li>${env==='dev'?'<li><code>CRONSOUL_BASE_URL</code><span>https URL this console is served from</span></li><li><code>CRONSOUL_SECRET_KEY</code><span>32-byte key, or a path in CRONSOUL_SECRET_KEY_FILE</span></li><li><code>CRONSOUL_DB</code><span>path to a file-backed database</span></li><li><code>CRONSOUL_TLS_MODE</code><span>proxy behind a TLS terminator, or cert</span></li>':''}</ul><span class="runtime-env-sub">Startup refuses to run if any production requirement is missing. <a href="https://github.com/user4xn/cronsoul/blob/master/docs/CONTAINER.md" target="_blank" rel="noopener noreferrer">Container &amp; environment guide ↗</a></span></div></section></div><div class="runtime-overview runtime-controls"><section class="card"><div class="runtime-card-head"><div><span class="runtime-eyebrow">SCHEDULING</span><h2>Automatic scheduler</h2></div><span class="pill ${sched?'success':'warning'}"><i class="dot"></i>${sched?'Enabled':'Disabled'}</span></div><p class="muted">When enabled, cron schedules run in the background. Manual Play remains available when disabled.</p>${sched?'':`<p class="runtime-note">Every new instance starts with the scheduler <b>off</b>, including production — that is the default, not a configuration error. Enable it once endpoints and the allowlist are ready; the choice is audited and survives restarts.</p>`}<button class="primary" id="toggle-scheduler">${sched?'Disable scheduler':'Enable scheduler'}</button></section><section class="card"><div class="runtime-card-head"><div><span class="runtime-eyebrow">EGRESS EXCEPTION</span><h2>Insecure HTTP test mode</h2></div><span class="pill ${sec.allow_insecure_targets?'warning':'success'}"><i class="dot"></i>${sec.allow_insecure_targets?'Active':'Off'}</span></div><p class="muted">Temporarily permits HTTP targets. The exception expires automatically and is unavailable in production.</p>${/^prod/i.test(env)?`<p class="runtime-note">Not available in production: plain-HTTP targets are always refused here. Use HTTPS endpoints.</p>`:''}<div class="actions">${['1h','24h','7d'].map(d=>`<button data-insecure="${d}"${/^prod/i.test(env)?' disabled title="Unavailable in production"':''}>Enable for ${d}</button>`).join('')}</div></section><section class="card runtime-timezone"><div class="runtime-card-head"><div><span class="runtime-eyebrow">DISPLAY PREFERENCE</span><h2>Console timezone</h2></div><span class="pill neutral"><i></i><span id="tz-zone">${esc(srvClock.zone)}</span></span></div><p class="muted">Timestamps in the console use this zone. Each endpoint schedule keeps its own timezone.</p><label class="tz-label" for="tz-select">Display timezone</label><div class="tz-row"><select id="tz-select"></select><button class="primary" id="tz-save">Save timezone</button></div><div class="tz-current"><span>Currently configured</span><code>${esc(state.settings['display.timezone']||'Asia/Jakarta')}</code></div></section><section class="card"><div class="runtime-card-head"><div><span class="runtime-eyebrow">LOCAL PREFERENCE</span><h2>Notification duration</h2></div><span class="pill neutral">This browser</span></div><p class="muted">Toast notifications can also be dismissed manually with ×.</p><label class="tz-label" for="toast-duration-select">Keep notifications visible for</label><select id="toast-duration-select"><option value="2000"${toast===2000?' selected':''}>2 seconds</option><option value="3000"${toast===3000?' selected':''}>3 seconds</option><option value="4000"${toast===4000?' selected':''}>4 seconds</option><option value="6000"${toast===6000?' selected':''}>6 seconds</option></select><p class="hint">Saved in this browser only · current: <span id="toast-duration-current">${toast/1000} seconds</span></p></section></div><section class="card build-card"><div class="runtime-card-head"><div><span class="runtime-eyebrow">DEPLOYMENT</span><h2>Running build</h2></div><span class="pill neutral">Embedded UI</span></div><p class="muted">Compare this version with your release before reporting a stale page. Hard refresh after deploying an update.</p><code class="mono" id="build-version">…</code><p class="build-links"><a href="https://github.com/user4xn/cronsoul" target="_blank" rel="noopener noreferrer">Repository</a><span aria-hidden="true">·</span><a href="https://github.com/user4xn/cronsoul/blob/master/README.md" target="_blank" rel="noopener noreferrer">README</a><span aria-hidden="true">·</span><a href="https://github.com/user4xn/cronsoul/blob/master/docs/HOW-TO-USE.md" target="_blank" rel="noopener noreferrer">How to use</a><span aria-hidden="true">·</span><a href="https://github.com/user4xn/cronsoul/blob/master/docs/CONTAINER.md" target="_blank" rel="noopener noreferrer">Container &amp; env</a></p></section>${runtimeSettingRows(state.settings,env)}`;}document.querySelectorAll('[data-play]').forEach(b=>b.onclick=()=>play(b.dataset.play));document.querySelectorAll('[data-edit]').forEach(b=>b.onclick=()=>{const j=state.jobs.find(x=>x.id===b.dataset.edit);if(j)endpointForm(j)});document.querySelectorAll('[data-run]').forEach(b=>b.onclick=()=>runDetail(b.dataset.run));document.querySelectorAll('[data-test]').forEach(b=>b.onclick=async()=>{const id=b.dataset.test,res=document.getElementById('ch-res-'+id);b.disabled=true;const label=b.textContent;b.textContent='Sending…';if(res){res.textContent='';res.className='ch-result'}try{const r=await api('/channels/'+id+'/test',{method:'POST'});if(res){res.className='ch-result '+(r.sent?'ok':'bad');res.textContent=r.sent?('Delivered to '+r.delivered+' of '+r.total+' target'+(r.total===1?'':'s')):(r.reason||r.error||'Delivery failed')}notify(r.sent?'Test notification delivered':(r.reason||'Delivery failed'))}catch(e){if(res){res.className='ch-result bad';res.textContent=e.message}notify(e.message)}finally{b.disabled=false;b.textContent=label}});if($('#add-channel'))$('#add-channel').onclick=()=>channelModal();if($('#add-channel-empty'))$('#add-channel-empty').onclick=()=>channelModal();settingsAction();governanceAction()}
function shellSplit(str){const out=[];let cur='',q=null;for(let i=0;i<str.length;i++){const c=str[i];if(q){if(c===q){q=null;continue}if(c==='\\'&&q==='"'&&i+1<str.length){cur+=str[++i];continue}cur+=c}else{if(c==='"'||c==="'"){q=c;continue}if(/\s/.test(c)){if(cur){out.push(cur);cur=''}continue}cur+=c}}if(cur)out.push(cur);return out}

function slug(v){return String(v||'').toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-+|-+$/g,'').slice(0,50)}

function parseCurl(text){
  const raw=String(text||'').replace(/\\\r?\n/g,' ').replace(/\r?\n/g,' ').trim();
  const warn=[];
  if(!/^curl\b/i.test(raw))return {error:'Start the line with "curl".'};
  const t=shellSplit(raw).slice(1);
  let method='',url='',headers={},body=null,user=null,bearer=null,formData=false,getMode=false,hasCookie=false;
  for(let i=0;i<t.length;i++){
    const a=t[i];
    const next=()=>t[++i];
    if(a==='-X'||a==='--request'){method=(next()||'').toUpperCase();continue}
    if(a==='-H'||a==='--header'){const h=next()||'';const k=h.indexOf(':');if(k>0){const name=h.slice(0,k).trim(),val=h.slice(k+1).trim();if(/^authorization$/i.test(name)){const m=val.match(/^Bearer\s+(.+)$/i);if(m)bearer=m[1];else if(/^Basic\s+/i.test(val))warn.push('Basic auth in header: enter the password in the form.');}else headers[name]=val}continue}
    if(a==='-d'||a==='--data'||a==='--data-raw'||a==='--data-binary'||a==='--data-ascii'){body=next()||'';continue}
    if(a==='-u'||a==='--user'){user=next()||'';continue}
    if(a==='-F'||a==='--form'){formData=true;next();continue}
    if(a==='-b'||a==='--cookie'){hasCookie=true;next();continue}
    if(a==='-G'||a==='--get'){getMode=true;continue}
    if(a==='--url'){url=next()||'';continue}
    if(a==='-L'||a==='--location'){warn.push('Redirects are never followed by CronSoul; -L ignored.');continue}
    if(a==='-k'||a==='--insecure'){warn.push('TLS verification stays on; -k ignored.');continue}
    if(a==='--compressed'||a==='-s'||a==='-sS'||a==='-i'||a==='-v'||a==='--verbose')continue;
    if(a.startsWith('-')){continue}
    if(!url)url=a;
  }
  if(!url)return {error:'No URL found in the command.'};
  let parsed;try{parsed=new URL(url)}catch{return {error:'URL is not absolute.'}}
  const query={};parsed.searchParams.forEach((v,k)=>{query[k]=v});
  parsed.search='';
  let bodyValue=null;
  if(body!==null){
    const trimmed=body.trim();
    if(trimmed.startsWith('{')||trimmed.startsWith('[')){try{bodyValue=JSON.parse(trimmed)}catch{warn.push('Body looked like JSON but did not parse; left empty.')}}
    else if(!formData){warn.push('Body is not JSON; CronSoul only sends JSON bodies, so it was left empty.')}
  }
  if(formData)warn.push('multipart/form-data is not supported; fields were skipped.');
  if(hasCookie)warn.push('Cookie headers are not imported; use an auth config instead.');
  const parts=parsed.pathname.split('/').filter(Boolean);const seg=(parts.slice(-2).join(' ')||parsed.hostname.split('.')[0]);
  const name=seg.replace(/[-_]+/g,' ').replace(/\b\w/g,c=>c.toUpperCase());
  const auth = bearer?{type:'bearer',config:{token:bearer}}:(user?{type:'basic',config:{username:user.split(':')[0],password:user.split(':').slice(1).join(':')}}:null);
  return {method:method||(body!==null?'POST':'GET'),url:parsed.origin+parsed.pathname,query:Object.keys(query).length?query:null,headers:Object.keys(headers).length?headers:null,body:bodyValue,name,key:slug(name)||'endpoint',service:parsed.hostname.split('.').slice(-2)[0]||'internal',auth,warn};
}

function applyCurl(p){
  const set=(id,v)=>{const el=document.getElementById(id);if(el&&v!==undefined&&v!==null)el.value=typeof v==='string'?v:JSON.stringify(v,null,0)};
  set('f-name',p.name);set('f-key',p.key);set('f-service',p.service);set('f-method',p.method);set('f-url',p.url);
  set('f-query',p.query?JSON.stringify(p.query):'');set('f-headers',p.headers?JSON.stringify(p.headers):'');set('f-body',p.body?JSON.stringify(p.body):'');
  window.__curlAuth=p.auth||null;window.__curlHeaders=Object.keys(p.headers||{});
  const badge=document.getElementById('curl-auth');
  if(badge)badge.innerHTML=p.auth?`<span class="pill success"><i></i>${esc(p.auth.type)} detected</span> <span class="hint">Stored encrypted, never returned.</span>`:'<span class="hint">No authorization header found.</span>';
}


function describeCron(expr){
  const raw=String(expr||'').trim();
  const p=raw.split(/\s+/);
  if(raw==='')return {text:'No schedule yet — enter five fields.',bad:true};
  if(p.length!==5)return {text:`Needs exactly five fields, found ${p.length}: minute hour day month weekday.`,bad:true};
  const [mi,ho,dom,mon,dow]=p;
  const MON={jan:1,feb:2,mar:3,apr:4,may:5,jun:6,jul:7,aug:8,sep:9,oct:10,nov:11,dec:12};
  const DOW={sun:0,mon:1,tue:2,wed:3,thu:4,fri:5,sat:6};
  const names=['Sun','Mon','Tue','Wed','Thu','Fri','Sat'];
  const months=['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
  const norm=(t,map)=>{const n=map&&map[t.toLowerCase()];return n!==undefined?String(n):t};
  const expand=(field,min,max,map)=>{
    const out=new Set();
    for(const piece of String(field).split(',')){
      const m=piece.match(/^(\*|[\w]+(?:-[\w]+)?)(?:\/(\d+))?$/);
      if(!m)return null;
      const base=m[1],step=m[2]?Number(m[2]):1;
      if(step<1)return null;
      let lo,hi;
      if(base==='*'){lo=min;hi=max}
      else{
        const range=base.split('-');
        lo=Number(norm(range[0],map));hi=range[1]!==undefined?Number(norm(range[1],map)):lo;
        if(Number.isNaN(lo)||Number.isNaN(hi)||lo<min||hi>max)return null;
        if(range[1]===undefined&&m[2]){hi=max}
      }
      for(let v=lo;v<=hi;v+=step)out.add(v);
      if(out.size>62)return null;
    }
    return [...out].sort((a,b)=>a-b);
  };
  const mins=expand(mi,0,59),hours=expand(ho,0,23),doms=expand(dom,1,31),mons=expand(mon,1,12,MON),dows=expand(dow,0,6,DOW);
  if(!mins||!hours||!doms||!mons||!dows)return {text:'Invalid value — check ranges and step limits per field.',bad:true};
  const pad=n=>String(n).padStart(2,'0');
  const list=arr=>arr.length===1?String(arr[0]):arr.slice(0,-1).join(', ')+' and '+arr[arr.length-1];
  // collapse contiguous runs of three or more into "a to b"
  const span=(arr,fmt)=>{const out=[];let i=0;while(i<arr.length){let j=i;while(j+1<arr.length&&arr[j+1]-arr[j]===1)j++;if(j-i>=2)out.push(`${fmt(arr[i])} to ${fmt(arr[j])}`);else for(let k=i;k<=j;k++)out.push(fmt(arr[k]));i=j+1}return out};
  const step=a=>{
    if(a.length<2)return null;
    const d=a[1]-a[0];
    if(d<1)return null;
    for(let i=2;i<a.length;i++)if(a[i]-a[i-1]!==d)return null;
    return d;
  };
  const timeStepMin=/^\*\/(\d+)$/.test(mi)?Number(mi.slice(2)):null;
  const timeStepHour=/^\*\/(\d+)$/.test(ho)?Number(ho.slice(2)):null;

  let time;
  if(mi==='*'&&ho==='*')time='every minute';
  else if(timeStepMin&&ho==='*')time=`every ${timeStepMin} minute${timeStepMin===1?'':'s'}`;
  else if(mi==='*')time=`every minute during hour${hours.length===1?'':'s'} ${list(hours.map(pad))}`;
  else if(ho==='*'&&mins.length===1)time=`every hour at minute ${pad(mins[0])}`;
  else if(ho==='*')time=`every hour at minute${mins.length===1?'':'s'} ${list(span(mins,pad))}`;
  else if(timeStepHour&&mi==='0')time=`every ${timeStepHour} hour${timeStepHour===1?'':'s'} on the hour`;
  else{
    const slots=[];
    for(const h of hours)for(const m of mins)slots.push(`${pad(h)}:${pad(m)}`);
    if(slots.length<=6)time=`at ${list(slots)}`;
    else{
      const hs=step(hours);
      if(hs&&mins.length===1)time=`every ${hs} hours at minute ${pad(mins[0])}`;
      else if(hours.length===1)time=`at minute${mins.length===1?'':'s'} ${list(span(mins,pad))} of hour ${pad(hours[0])}`;
      else time=`at minute${mins.length===1?'':'s'} ${list(span(mins,pad))} of hours ${list(span(hours,pad))}`;
    }
  }

  const dowPart=dow!=='*'?(dows.length===7?'every day':`on ${list(span(dows,d=>names[d]))}`):'';
  const domPart=dom!=='*'?`day${doms.length>1?'s':''} ${list(span(doms,String))} of the month`:'';
  const monPart=mon!=='*'?`in ${list(span(mons,m=>months[m-1]))}`:'';
  let dayText;
  if(dowPart&&domPart)dayText=`${dowPart} that falls on ${domPart}${monPart?' '+monPart:''}`;
  else if(domPart)dayText=`on ${domPart}${monPart?' '+monPart:''}`;
  else dayText=[dowPart,monPart].filter(Boolean).join(' ');
  let text=`Runs ${time}${dayText?' '+dayText:', every day'}`;
  text=text[0].toUpperCase()+text.slice(1);
  const notes=[];
  if(dom!=='*'&&dow!=='*')notes.push('day-of-month and weekday are both restricted, so both must match');
  if(mons.length===12&&mon!=='*')notes.push('every month listed');
  return {text:text+(notes.length?` (${notes.join('; ')})`:'')+'.'};
}
function wireCron(){
  const input=document.getElementById('f-cron');if(!input)return;
  const out=document.getElementById('cron-desc');
  const paint=()=>{const d=describeCron(input.value);out.textContent='Reads as: '+(d.bad?d.text:cronProse(input.value));out.className='hint'+(d.bad?' err':' ok');};
  input.addEventListener('input',paint);paint();
  document.querySelectorAll('#cron-chips [data-cron]').forEach(b=>b.onclick=()=>{input.value=b.dataset.cron;paint();input.focus()});
}
function wireCurl(){
  const box=document.getElementById('curl-in');if(!box)return;
  const run=()=>{
    const out=document.getElementById('curl-hint');
    if(!box.value.trim()){out.textContent='';out.className='hint';return}
    const p=parseCurl(box.value);
    if(p.error){out.textContent=p.error;out.className='hint err';return}
    applyCurl(p);
    out.className='hint ok';
    out.textContent=`Filled: ${p.method} ${p.url}`+(p.headers?` · ${Object.keys(p.headers).length} headers`:'')+(p.query?` · ${Object.keys(p.query).length} query`:'')+(p.body?' · JSON body':'')+(p.warn.length?` — ${p.warn.join(' ')}`:'');
  };
  box.addEventListener('paste',()=>setTimeout(run,30));
  box.addEventListener('input',()=>{if(box.value.trim().length>6)run()});
  const btn=document.getElementById('curl-parse');if(btn)btn.onclick=run;
}


function fillEndpointForm(j){
  const set=(sel,v)=>{const el=document.querySelector(sel);if(el)el.value=v??''};
  const check=(sel,v)=>{const el=document.querySelector(sel);if(el)el.checked=!!v};
  const h2=document.querySelector('.form-card h2');if(h2)h2.textContent='Edit endpoint';
  const sub=document.querySelector('.form-card .form-head p');
  if(sub)sub.textContent='Changes apply to the next scheduled run. Credentials stay untouched unless you paste a new one.';
  const submit=document.querySelector('.form-actions button.primary');
  if(submit)submit.textContent='Save changes';
  set('#f-name',j.name);set('#f-key',j.key);set('#f-service',j.service);
  set('#f-method',j.method);set('#f-url',j.url);set('#f-cron',j.cron);set('#f-tz',j.timezone||'Asia/Jakarta');
  const js=v=>v&&Object.keys(v).length?JSON.stringify(v,null,2):'';
  set('#f-headers',js(j.headers));set('#f-query',js(j.query));set('#f-body',js(j.body));
  if(j.retry)set('#f-retry',j.retry.max??0);
  if(j.retry)set('#f-backoff',j.retry.backoff||'exponential');
  check('input[name=idempotent]',j.idempotent);
  check('input[name=clock_aware]',j.clock_aware);
  set('#f-sim',j.simulated_time_header||'');
  set('#f-dry',j.dry_run_param||'');
  if(j.notify){set('#f-mail',(j.notify.emails||[]).join(', '));set('#f-tg',(j.notify.telegram||[]).join(', '));
    const sel=document.querySelector('#f-events');
    if(sel)[...sel.options].forEach(o=>{o.selected=(j.notify.on||[]).includes(o.value)})}
}
function endpointForm(job){
  const c=$('#content');
  const act=document.querySelector('.page-head .primary');if(act)act.remove();
  c.innerHTML=`<div class="card form-card form-v2">
    <div class="form-head">
      <div>
        <h2>${job?'Edit endpoint':'New endpoint'}</h2>
        <p class="muted">CronSoul stores the address, schedule and policy. Your service keeps doing the work.</p>
      </div>
      <div class="head-meta"><span class="pill neutral"><i></i>${job?'Editing':'New'}</span></div>
    </div>
    <form id="endpoint" novalidate>
      <div class="form-layout">
        <div class="form-main">
          <div class="tabs" role="tablist" aria-label="Endpoint sections">
            <button type="button" class="tab active" role="tab" data-tab="request">${iconSet.link||''}Request</button>
            <button type="button" class="tab" role="tab" data-tab="schedule">Schedule</button>
            <button type="button" class="tab" role="tab" data-tab="resilience">Resilience</button>
            <button type="button" class="tab" role="tab" data-tab="assertions">Assertions<span class="tab-count" id="assert-count">0</span></button>
            <button type="button" class="tab" role="tab" data-tab="simulation">Simulation</button>
          </div>
          <div class="tabpanels">
            <section class="tp active" data-tp="request">
              <details class="fs paste-zone" ${job?'':'open'}>
                <summary><h3>Paste a cURL</h3><span class="muted">Fill the form from a command</span></summary>
                <div class="fs-body">
                  <textarea id="curl-in" spellcheck="false" placeholder="curl -X POST 'https://api.internal.example.com/v1/sync' -H 'Authorization: Bearer ***' -H 'Content-Type: application/json' -d '{"day":"today"}'"></textarea>
                  <div class="paste-row"><span id="curl-hint" class="hint">Paste a command and the form fills itself.</span><span id="curl-auth"></span><button type="button" id="curl-parse">Fill form</button></div>
                </div>
              </details>
              <div class="sect-title"><h3>Request</h3><span class="muted">Name, key and destination</span></div>
              <div class="fgrid">
                <div class="field"><label for="f-name">Display name <span class="req">required</span></label><input id="f-name" name="name" placeholder="Sync orders" required><span class="hint">How this endpoint is listed in the console.</span></div>
                <div class="field"><label for="f-key">Key <span class="req">required</span></label><input id="f-key" name="key" placeholder="sync-orders" pattern="[a-z0-9][a-z0-9_-]{1,63}" required><span class="hint">Lowercase, unique, used in logs and templates.</span></div>
              </div>
              <div class="fgrid">
                <div class="field"><label for="f-service">Service <span class="req">required</span></label><input id="f-service" name="service" placeholder="orders" required></div>
                <div class="field"><label for="f-method">Method</label><select id="f-method" name="method"><option>GET</option><option>POST</option><option>PUT</option><option>PATCH</option><option>DELETE</option></select><span class="hint">Overrides the verb detected from cURL.</span></div>
              </div>
              <div class="field wide"><label for="f-url">URL <span class="req">required</span></label><input id="f-url" name="url" type="url" placeholder="https://api.internal.example.com/health" required><span class="hint">Reachable from this instance and permitted by the egress allowlist.</span><div id="egress-check" class="egress-check" aria-live="polite" hidden></div></div>
              <details class="fs">
                <summary><h3>Payload</h3><span class="muted">Headers, query and body</span></summary>
                <div class="fs-body">
                  <div class="fgrid">
                    <div class="field"><label for="f-headers">Headers (JSON)</label><textarea id="f-headers" name="headers" spellcheck="false" placeholder='{"Accept":"application/json"}'></textarea><span class="hint">Sent as-is. Never put long-lived secrets here — use endpoint auth.</span></div>
                    <div class="field"><label for="f-query">Query (JSON)</label><textarea id="f-query" name="query" spellcheck="false" placeholder='{"env":"staging"}'></textarea></div>
                  </div>
                  <div class="field wide"><label for="f-body">Body (JSON)</label><textarea id="f-body" name="body" spellcheck="false" placeholder='{"ok":true}'></textarea><span class="hint">Object or array. Templates like {{job.key}} and {{simulated_time}} expand at run time.</span></div>
                </div>
              </details>
            </section>

            <section class="tp" data-tp="schedule">
              <div class="sect-title"><h3>Schedule</h3><span class="muted">Five-field cron, evaluated in the endpoint timezone</span></div>
              <div class="fgrid">
                <div class="field"><label for="f-cron">Cron</label><input id="f-cron" name="cron" value="*/5 * * * *" spellcheck="false" autocomplete="off" required><span class="hint" id="cron-desc" aria-live="polite">Reads as: every 5 minutes.</span><div class="chips" id="cron-chips"><button type="button" data-cron="*/5 * * * *">Every 5 min</button><button type="button" data-cron="0 * * * *">Hourly</button><button type="button" data-cron="0 9 * * 1-5">Weekdays 09:00</button><button type="button" data-cron="0 0 * * *">Daily 00:00</button><button type="button" data-cron="0 3 * * 0">Sunday 03:00</button></div></div>
                <div class="field"><label for="f-tz">Timezone</label><input id="f-tz" name="timezone" value="Asia/Jakarta" required><span class="hint">IANA name, e.g. Asia/Jakarta. Cron is evaluated in this zone.</span></div>
              </div>
              <div class="rail-note">The right-hand preview calls the server, so the dates below are exactly what the scheduler will use.</div>
            </section>

            <section class="tp" data-tp="resilience">
              <div class="sect-title"><h3>Resilience</h3><span class="muted">What happens when the endpoint is slow or down</span></div>
              <div class="fgrid">
                <div class="field"><label for="f-retry">Retry attempts</label><input id="f-retry" name="retry_max" type="number" min="0" max="5" value="0"><span class="hint">0 to 5. Only transient failures are retried.</span></div>
                <div class="field"><label for="f-backoff">Backoff</label><select id="f-backoff" name="retry_backoff"><option value="exponential">exponential</option><option value="fixed">fixed</option></select><span class="hint">Exponential doubles the gap: 2s, 4s, 8s… Fixed waits the same each time.</span></div>
              </div>
              <label class="switch-row" for="f-idem"><input type="checkbox" id="f-idem" name="idempotent"><span><strong>Idempotent</strong><small>Safe to retry write requests. Leave off for POSTs that would duplicate work.</small></span></label>
              <div class="rail-note">Alerts are configured once per notification channel — a channel decides which events it receives, so nothing has to be repeated on every endpoint.</div>
            </section>

            <section class="tp" data-tp="assertions">
              <div class="sect-title"><h3>Assertions</h3><span class="muted">Optional checks QA can rely on</span></div>
              <p class="hint">A run still succeeds on HTTP 2xx/3xx. Add rules here when the status code alone is not enough — a failed rule marks the run as failed and can trigger an alert.</p>
              <div id="assert-rows"></div>
              <button type="button" class="ghost" id="assert-add">+ Add rule</button>
              <div id="assert-hint" class="hint" style="margin-top:8px"></div>
            </section>

            <section class="tp" data-tp="simulation">
              <div class="sect-title"><h3>Clock and simulation</h3><span class="muted">Only needed when QA replays a job at another time</span></div>
              <label class="switch-row" for="f-clock"><input type="checkbox" id="f-clock" name="clock_aware"><span><strong>Clock-aware</strong><small>Lets QA send a simulated timestamp for this endpoint. Without this, the server ignores simulated time for this job.</small></span></label>
              <div class="fgrid">
                <div class="field"><label for="f-sim">Simulated time header</label><input id="f-sim" name="simulated_time_header" placeholder="X-Simulated-Time"><span class="hint">Header CronSoul sends (RFC3339) when QA picks a simulated time.</span></div>
                <div class="field"><label for="f-dry">Dry-run parameter</label><input id="f-dry" name="dry_run_param" placeholder="dry_run"><span class="hint">Query parameter appended for dry runs, so the endpoint can validate without writing.</span></div>
              </div>
              <div class="rail-note">Simulation is opt-in per endpoint. In production the run also needs <code>allow_simulated_time_prod</code>, so a stray header can never rewrite data.</div>
              ${simGuideHtml()}
            </section>
          </div>
        </div>

        <aside class="form-rail">
          <div class="rail-card">
            <h4>Summary</h4>
            <dl class="rail-list">
              <div><dt>Endpoint</dt><dd id="r-name">—</dd></div>
              <div><dt>Request</dt><dd id="r-req" class="mono">—</dd></div>
              <div><dt>Schedule</dt><dd id="r-sched">—</dd></div>
              <div><dt>Timezone</dt><dd id="r-tz">Asia/Jakarta</dd></div>
              <div><dt>Retry</dt><dd id="r-retry">none</dd></div>
              <div><dt>Assertions</dt><dd id="r-assert">none</dd></div>
            </dl>
          </div>
          <div class="rail-card">
            <h4>Next runs</h4>
            <ol class="rail-runs" id="r-runs"><li class="dim">Type a cron expression…</li></ol>
            <span class="hint" id="r-runs-hint"></span>
          </div>
          <div class="rail-card">
            <h4>Connection test</h4>
            <p class="hint">Calls the URL once with the stored policy and shows the result. No run is recorded.</p>
            <button type="button" class="ghost wide" id="r-test" ${job?'':'disabled'}><svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" style="vertical-align:-2px"><path d="M13 3 5 14h6l-1 7 8-11h-6z"/></svg> Test connection</button>
            <div id="r-test-out" class="hint"></div>
          </div>
          ${job?`<div class="rail-card muted-card"><h4>Meta</h4><dl class="rail-list"><div><dt>Key</dt><dd class="mono">${esc(job.key)}</dd></div><div><dt>Updated</dt><dd>${stamp(job.updated_at)}</dd></div><div><dt>Tier</dt><dd>${tierBadge(job.risk_tier)} ${esc((TIER_INFO[String(job.risk_tier||'A').toUpperCase()]||[''])[0])}</dd></div></dl></div>`:''}
        </aside>
      </div>
      <div class="form-foot">
        <span class="muted">Changes apply to the next scheduled run. Credentials stay untouched unless you replace them.</span>
        <div class="foot-actions">
          <button type="button" class="ghost" onclick="render()">Discard</button>
          <button class="primary" type="submit">${job?'Save changes':'Create endpoint'}</button>
        </div>
      </div>
    </form>
  </div>`;

  wireCurl();wireCron();
  if(job)fillEndpointForm(job);
  document.getElementById('f-cron')?.dispatchEvent(new Event('input'));
  document.getElementById('f-tz')?.dispatchEvent(new Event('input'));
  wireFormRail(job);
  wireSimGuide();
  wireEgressCheck();

  $('#endpoint').onsubmit=async e=>{
    e.preventDefault();
    const f=new FormData(e.target),x=Object.fromEntries(f);
    x.timeout_s=30;x.risk_tier=job?job.risk_tier||'A':'A';x.enabled=job?job.enabled!==false:true;
    if(window.__curlAuth&&!job&&!job?.auth_type)x.auth=window.__curlAuth;
    x.idempotent=f.has('idempotent');x.clock_aware=f.has('clock_aware');
    x.retry={max:Number(x.retry_max||0),backoff:x.retry_backoff||'exponential'};
    delete x.retry_max;delete x.retry_backoff;
    if(!x.simulated_time_header)delete x.simulated_time_header;
    if(!x.dry_run_param)delete x.dry_run_param;
    try{ x.assertions=collectAssertions(); }catch(err){notify(err.message);return}
    for(const k of ['headers','query','body'])if(x[k]){try{x[k]=JSON.parse(x[k])}catch{notify(k+' must be valid JSON');return}}else delete x[k];
    try{
      await api(job?'/jobs/'+job.id:'/jobs/',{method:job?'PUT':'POST',body:JSON.stringify(x)});
      notify(job?'Endpoint updated':'Endpoint created');
      state.page='endpoints';render();
    }catch(err){notify(err.message)}
  };
}

// Checks the URL against the live egress policy while the form is open, so an
// allowlist gap shows up here instead of as a failed first run.
function wireEgressCheck(){
  const input=$('#f-url'),box=$('#egress-check');if(!input||!box)return;
  let timer=null,seq=0;
  const show=(cls,html)=>{box.className='egress-check '+cls;box.innerHTML=html;box.hidden=false};
  const check=async()=>{
    const raw=input.value.trim();
    if(!/^https?:\/\/[^/\s]+/i.test(raw)){box.hidden=true;return}
    const mine=++seq;
    let r;try{r=await api('/egress/check?url='+encodeURIComponent(raw))}catch{if(mine===seq)box.hidden=true;return}
    if(mine!==seq)return;
    if(r.allowed){show('ok',`<span class="pill good"><i></i>allowed</span><span><b class="mono">${esc(r.host)}</b> is on the egress allowlist.</span>`);return}
    if(!r.not_allowlisted){show('bad',`<span class="pill bad"><i></i>blocked</span><span>${esc(r.reason||'Blocked by egress policy')}</span>`);return}
    const isSuper=state.me?.role==='superadmin';
    const lead=r.allowlist_empty?'The allowlist is empty, so every run is refused until a host is added.':`<b class="mono">${esc(r.host)}</b> is not on the egress allowlist; runs will be refused.`;
    show('warn',`<span class="pill warn"><i></i>not allowed</span><span>${lead} ${isSuper?'':'Ask a superadmin to add it.'}</span>${isSuper?`<button type="button" class="ghost" id="egress-add">Add <span class="mono">${esc(r.suggested_pattern)}</span> to allowlist</button>`:''}`);
    const add=$('#egress-add');
    if(add)add.onclick=async()=>{
      add.disabled=true;
      try{await api('/allowlist',{method:'POST',body:JSON.stringify({pattern:r.suggested_pattern,note:'Added from endpoint form'})});notify('Allowlisted '+r.suggested_pattern);check()}
      catch(e){if(/already exists/i.test(e.message))check();else{notify(e.message);add.disabled=false}}
    };
  };
  const schedule=()=>{clearTimeout(timer);timer=setTimeout(check,350)};
  input.addEventListener('input',schedule);input.addEventListener('change',schedule);
  $('#curl-parse')?.addEventListener('click',()=>setTimeout(check,50));
  check();
}

const SIM_LANGS=[['go','Go'],['php','PHP'],['js','Node.js']];

function simGuideCode(lang,header,param){
  const h=header||'X-Simulated-Time',p=param||'dry_run';
  if(lang==='go')return `// net/http
func syncOrders(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	if v := r.Header.Get("${h}"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "bad ${h}", http.StatusBadRequest)
			return
		}
		now = t // use this clock for every date decision
	}
	dryRun := r.URL.Query().Get("${p}") == "true"

	orders, err := findDueOrders(r.Context(), now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !dryRun {
		if err := markSynced(r.Context(), orders); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok": true, "now": now.Format(time.RFC3339),
		"dry_run": dryRun, "would_process": len(orders),
	})
}`;
  if(lang==='php')return `<?php
// plain PHP; in Laravel use $request->header('${h}') / $request->query('${p}')
$header = $_SERVER['HTTP_${h.toUpperCase().replace(/-/g,'_')}'] ?? null;
$now = new DateTimeImmutable('now');
if ($header !== null) {
    $parsed = DateTimeImmutable::createFromFormat(DATE_RFC3339, $header);
    if ($parsed === false) {
        http_response_code(400);
        exit('bad ${h}');
    }
    $now = $parsed; // use this clock for every date decision
}
$dryRun = ($_GET['${p}'] ?? '') === 'true';

$orders = findDueOrders($now);
if (!$dryRun) {
    markSynced($orders);
}

header('Content-Type: application/json');
echo json_encode([
    'ok' => true,
    'now' => $now->format(DATE_RFC3339),
    'dry_run' => $dryRun,
    'would_process' => count($orders),
]);`;
  return `// Express
app.post('/v1/sync', async (req, res) => {
  let now = new Date();
  const header = req.get('${h}');
  if (header) {
    const t = new Date(header);
    if (Number.isNaN(t.getTime())) return res.status(400).send('bad ${h}');
    now = t; // use this clock for every date decision
  }
  const dryRun = req.query['${p}'] === 'true';

  const orders = await findDueOrders(now);
  if (!dryRun) await markSynced(orders);

  res.json({
    ok: true,
    now: now.toISOString(),
    dry_run: dryRun,
    would_process: orders.length,
  });
});`;
}

function simGuideHtml(){
  return `<details class="fs sim-guide">
    <summary><h3>Make your endpoint support this</h3><span class="muted">What CronSoul sends and how to read it</span></summary>
    <div class="fs-body">
      <ul class="sim-rules">
        <li><strong>Simulated time</strong> — when QA runs this endpoint with a simulated time, CronSoul sends <code id="sg-h">X-Simulated-Time</code> with an RFC3339 value, e.g. <code>2026-01-31T23:59:00+07:00</code>. Scheduled runs never send it. Templates <code>{{simulated_time}}</code>, <code>{{now}}</code>, <code>{{date}}</code> and <code>{{unix}}</code> in headers, query and body use the same clock.</li>
        <li><strong>Dry run</strong> — CronSoul adds <code>?<span id="sg-p">dry_run</span>=true</code> to the URL. Do all reads and validation, skip every write and side effect (DB, queues, email, payments), and still answer 2xx so assertions can run.</li>
        <li><strong>To qualify</strong>, the endpoint must: take "now" from the header when it is present instead of the system clock, apply it everywhere a date is decided, reject a malformed value with 400, and never ignore the dry-run flag. Ideally only honour the header outside production, or behind an auth check.</li>
        <li>Return what you would have done (e.g. <code>would_process</code>) so QA can assert on it with a JSON-path rule.</li>
      </ul>
      <div class="sim-tabs" role="tablist" aria-label="Sample language">
        ${SIM_LANGS.map(([k,l],i)=>`<button type="button" class="seg-btn ${i===0?'active':''}" role="tab" data-sg-lang="${k}">${l}</button>`).join('')}
        <button type="button" class="ghost sg-copy" id="sg-copy">Copy</button>
      </div>
      <pre class="excerpt sim-code" id="sg-code"></pre>
    </div>
  </details>`;
}

function wireSimGuide(){
  const code=$('#sg-code');if(!code)return;
  let lang='go';
  const hIn=$('#f-sim'),pIn=$('#f-dry');
  const draw=()=>{
    const h=(hIn?.value||'').trim()||'X-Simulated-Time',p=(pIn?.value||'').trim()||'dry_run';
    $('#sg-h').textContent=h;$('#sg-p').textContent=p;
    code.textContent=simGuideCode(lang,h,p);
  };
  document.querySelectorAll('[data-sg-lang]').forEach(b=>b.onclick=()=>{
    lang=b.dataset.sgLang;
    document.querySelectorAll('[data-sg-lang]').forEach(x=>x.classList.toggle('active',x===b));
    draw();
  });
  hIn?.addEventListener('input',draw);pIn?.addEventListener('input',draw);
  $('#sg-copy').onclick=async()=>notify(await copyText(code.textContent)?'Sample copied':'Copy failed');
  draw();
}

const ASSERT_KINDS=[
  {k:'status_code',label:'Status code is',path:false,op:false,val:'200',ph:'200'},
  {k:'duration_ms',label:'Response time',path:false,op:true,val:'2000',ph:'2000'},
  {k:'body_contains',label:'Body contains',path:false,op:false,val:'',ph:'"ok":true'},
  {k:'json_path_exists',label:'JSON path exists',path:true,op:false,val:'',ph:'data.id'},
  {k:'json_path_equals',label:'JSON path equals',path:true,op:false,val:'',ph:'status'},
  {k:'json_path_gte',label:'JSON path at least',path:true,op:false,val:'1',ph:'count'},
  {k:'header_equals',label:'Header equals',path:true,op:false,val:'',ph:'X-Request-Id'}
];
function assertKind(k){return ASSERT_KINDS.find(a=>a.k===k)||ASSERT_KINDS[0]}
function assertRowHtml(rule){
  const r=rule||{kind:'status_code',value:'200'};
  const spec=assertKind(r.kind);
  const ops=['lt','lte','gt','gte'];
  const val=r.value!==undefined&&r.value!==null?r.value:(r.equals!==undefined?r.equals:'');
  return `<div class="assert-row" data-assert-row>
    <select data-a-kind>${ASSERT_KINDS.map(a=>`<option value="${a.k}" ${a.k===r.kind?'selected':''}>${a.label}</option>`).join('')}</select>
    ${spec.op?`<select data-a-op>${ops.map(o=>`<option ${o===(r.op||'lte')?'selected':''}>${o}</option>`).join('')}</select>`:''}
    ${spec.path?`<input data-a-path placeholder="${spec.ph||'path'}" value="${esc(r.path||'')}">`:''}
    <input data-a-val placeholder="${spec.ph||'value'}" value="${esc(String(val))}">
    <button type="button" class="row-action danger" data-a-del title="Remove">✕</button>
  </div>`;
}
function addAssertRow(rule){
  const host=$('#assert-rows');if(!host)return;
  const wrap=document.createElement('div');wrap.innerHTML=assertRowHtml(rule);
  const row=wrap.firstElementChild;
  row.querySelector('[data-a-kind]').onchange=()=>{
    const r={kind:row.querySelector('[data-a-kind]').value,value:row.querySelector('[data-a-val]').value};
    const tmp=document.createElement('div');tmp.innerHTML=assertRowHtml(r);
    row.replaceWith(tmp.firstElementChild);bindAssertRow(tmp.firstElementChild);updateAssertMeta();
  };
  row.querySelector('[data-a-del]').onclick=()=>{row.remove();updateAssertMeta()};
  host.appendChild(row);bindAssertRow(row);updateAssertMeta();
}
function bindAssertRow(row){
  row.querySelector('[data-a-kind]').onchange=()=>{
    const r={kind:row.querySelector('[data-a-kind]').value};
    const tmp=document.createElement('div');tmp.innerHTML=assertRowHtml(r);
    const nr=tmp.firstElementChild;row.replaceWith(nr);bindAssertRow(nr);updateAssertMeta();
  };
  row.querySelector('[data-a-del]').onclick=()=>{row.remove();updateAssertMeta()};
  row.querySelectorAll('input').forEach(i=>i.oninput=updateAssertMeta);
}
function updateAssertMeta(){
  const n=document.querySelectorAll('[data-assert-row]').length;
  const c=$('#assert-count');if(c)c.textContent=n;
  const r=$('#r-assert');if(r)r.textContent=n?`${n} rule${n===1?'':'s'}`:'none';
  const h=$('#assert-hint');
  if(h)h.textContent=n?'A failed rule marks the run as failed.':'No rules: any 2xx/3xx passes.';
}
function parseAssertValue(raw,kind){
  const s=String(raw==null?'':raw).trim();
  if(s==='')return '';
  if(kind==='status_code'||kind==='duration_ms'||kind==='json_path_gte'||kind==='json_path_lte'){
    const n=Number(s);if(!Number.isFinite(n))throw new Error('Assertion needs a numeric value');return n;
  }
  if(/^-?\d+(\.\d+)?$/.test(s))return Number(s);
  if(/^[[{"]/.test(s)){try{return JSON.parse(s)}catch{}}
  return s;
}
function collectAssertions(){
  const rows=[...document.querySelectorAll('[data-assert-row]')];
  const rules=rows.map(row=>{
    const kind=row.querySelector('[data-a-kind]').value;
    const spec=assertKind(kind);
    const rule={kind};
    const path=row.querySelector('[data-a-path]');
    if(spec.path){const p=(path?.value||'').trim();if(!p)throw new Error('Assertion needs a path');rule.path=p}
    const op=row.querySelector('[data-a-op]');if(op)rule.op=op.value;
    const rawVal=(row.querySelector('[data-a-val]')?.value||'').trim();
    if(kind==='json_path_exists'){/* value not required */}else if(rawVal===''){throw new Error('Assertion needs a value')}
    if(rawVal!=='')rule.value=parseAssertValue(rawVal,kind);
    return rule;
  });
  return {enabled:rules.length>0,rules};
}

function wireFormRail(job){
  const runsHost=$('#r-runs'),hint=$('#r-runs-hint');
  let timer=null,lastKey='';
  const refresh=async()=>{
    const cron=($('#f-cron')?.value||'').trim(),tz=($('#f-tz')?.value||'').trim()||'Asia/Jakarta';
    const key=cron+'|'+tz;if(key===lastKey)return;lastKey=key;
    if(!cron){runsHost.innerHTML='<li class="dim">Type a cron expression…</li>';if(hint)hint.textContent='';return}
    try{
      const r=await api('/jobs/schedule-preview',{method:'POST',body:JSON.stringify({cron,timezone:tz,count:3})});
      runsHost.innerHTML=(r.runs||[]).map(t=>`<li><span class="rel">${relTime(t)}</span><span class="abs">${stamp(t)}</span></li>`).join('')||'<li class="dim">No upcoming run.</li>';
      if(hint)hint.textContent=r.runs&&r.runs.length?`In ${r.timezone}. First run ${relTime(r.runs[0])}.`:'';
    }catch(err){
      runsHost.innerHTML='<li class="dim">—</li>';
      if(hint)hint.textContent=err.message;
    }
  };
  const scheduleRefresh=()=>{clearTimeout(timer);timer=setTimeout(refresh,220)};
  ['#f-cron','#f-tz'].forEach(sel=>{const el=$(sel);if(el){el.addEventListener('input',scheduleRefresh);el.addEventListener('change',scheduleRefresh)}});
  refresh();

  const upd=()=>{
    const name=$('#f-name')?.value||'—';
    const url=$('#f-url')?.value||'';
    const method=$('#f-method')?.value||'GET';
    const cron=$('#f-cron')?.value||'';
    const tz=$('#f-tz')?.value||'';
    const host=(()=>{try{return new URL(url).host}catch{return url.replace(/^https?:\/\//,'').split('/')[0]}})().slice(0,28);
    const max=$('#f-retry')?.value||'0';
    const backoff=$('#f-backoff')?.value||'exponential';
    if($('#r-name'))$('#r-name').textContent=name;
    if($('#r-req'))$('#r-req').textContent=`${method} ${host||'—'}`;
    if($('#r-sched'))$('#r-sched').textContent=cron?cronProse(cron):'—';
    if($('#r-tz'))$('#r-tz').textContent=tz||'—';
    if($('#r-retry'))$('#r-retry').textContent=Number(max)?`${max}× ${backoff==='fixed'?'fixed':'exp'}`:'none';
    updateAssertMeta();
  };
  ['#f-name','#f-url','#f-method','#f-cron','#f-tz','#f-retry','#f-backoff'].forEach(sel=>{const el=$(sel);if(el){el.addEventListener('input',upd);el.addEventListener('change',upd)}});
  document.querySelectorAll('[data-tab]').forEach(t=>t.onclick=()=>{
    document.querySelectorAll('[data-tab]').forEach(x=>{x.classList.toggle('active',x===t);x.setAttribute('aria-selected',x===t?'true':'false')});
    document.querySelectorAll('.tp').forEach(p=>p.classList.toggle('active',p.dataset.tp===t.dataset.tab));
  });
  $('#assert-add').onclick=()=>addAssertRow();
  (job?.assertions?.rules||[]).forEach(addAssertRow);
  upd();

  const tbtn=$('#r-test');
  if(tbtn&&job)tbtn.onclick=async()=>{
    const out=$('#r-test-out');tbtn.disabled=true;out.textContent='Calling…';
    try{
      const r=await api('/jobs/'+job.id+'/preview',{method:'POST',body:JSON.stringify({})});
      const ok=r.ok??(r.status>=200&&r.status<400);
      out.innerHTML=`<span class="pill ${ok?'good':'bad'}">${ok?'Reached':'Failed'}</span> HTTP ${r.status??'—'} · ${r.duration_ms??r.latency_ms??'—'} ms`;
    }catch(err){out.innerHTML=`<span class="pill bad">Failed</span> ${esc(err.message)}`}
    tbtn.disabled=false;
  };
}

async function copyText(text){
  try{await navigator.clipboard.writeText(text);return true}catch{}
  try{const ta=document.createElement('textarea');ta.value=text;ta.setAttribute('readonly','');ta.style.cssText='position:fixed;top:0;left:0;opacity:0';document.body.appendChild(ta);ta.select();const ok=document.execCommand('copy');ta.remove();return ok}catch{return false}
}
function buildCurl(job,run,effUrl){
  const m=String(job?.method||'GET').toUpperCase();
  let u=effUrl||job?.url||'';
  if(run?.dry_run&&job?.dry_run_param&&!u.includes(job.dry_run_param+'=')){
    u+=(u.includes('?')?'&':'?')+encodeURIComponent(job.dry_run_param)+'=true';
  }
  let out=`curl -X ${m} '${u}'`;
  const headers=Object.assign({},job?.headers||{});
  if(job?.auth_type==='bearer')headers['Authorization']='Bearer $TOKEN';
  else if(job?.auth_type==='api_key')headers['X-API-Key']='$API_KEY';
  else if(job?.auth_type==='basic')headers['Authorization']='Basic $BASIC_CREDENTIALS';
  else if(job?.auth_type==='custom_header'&&job?.auth_header)headers[job.auth_header]='$CUSTOM_SECRET';
  for(const [k,v] of Object.entries(headers))out+=` \\\n  -H '${k}: ${v}'`;
  if(run?.simulated_time)out+=` \\\n  # simulated time was ${run.simulated_time}`;
  if(job?.body&&Object.keys(job.body||{}).length)out+=` \\\n  --data '${JSON.stringify(job.body)}'`;
  return out;
}

function play(id){const j=state.jobs.find(x=>x.id===id);const c=$('#content');c.innerHTML=`<div class="card form"><h2>Play · ${esc(j?.name)}</h2><form id="play"><div class="field"><label>Simulated time (RFC3339, optional)</label><input name="simulated_time" placeholder="2026-01-02T03:04:05Z"></div><label><input type="checkbox" name="dry_run"> Dry run</label><div class="actions"><button type="button" onclick="render()">Cancel</button><button class="primary">▶ Run now</button></div></form></div>`;$('#play').onsubmit=async e=>{e.preventDefault();const x=Object.fromEntries(new FormData(e.target));x.dry_run=!!x.dry_run;Object.keys(x).forEach(k=>x[k]===''&&delete x[k]);try{const r=await api('/jobs/'+id+'/run',{method:'POST',body:JSON.stringify(x)});notify('Run queued: '+r.run_id.slice(0,8));state.page='runs';render()}catch(x){notify(x.message)}}}
function channelForm(){const c=$('#content');c.innerHTML=`<div class="card form"><h2>Add notification channel</h2><form id="channel"><div class="field"><label>Name</label><input name="name" required></div><div class="field"><label>Kind</label><select name="kind"><option value="telegram">Telegram</option><option value="smtp">SMTP</option></select></div><div id="channel-extra"></div><fieldset class="ev-field"><legend>Notify me when</legend>${[["failure","A run fails after all retries","A scheduled call finally gives up"],["recovered","A run succeeds after earlier failures","The endpoint came back"],["missed_run","A scheduled window was missed","The scheduler did not fire"],["assertion_failed","An assertion fails","QA checks on the response break"],["manual_run","A manual run (Play) succeeds","Confirms a run started by hand"]].map(([v,t,h])=>`<label class="ev"><input type="checkbox" name="events" value="${v}"${v==='manual_run'?'':' checked'}><span><b>${t}</b><small>${h}</small></span></label>`).join("")}</fieldset><div class="actions"><button type="button" onclick="render()">Cancel</button><button class="primary">Save channel</button></div></form></div>`;const kind=$('#channel select[name=kind]'), extra=$('#channel-extra');function fields(){extra.innerHTML=kind.value==='telegram'?'<div class=field><label>Bot token</label><input name=bot_token type=password required></div><div class=field><label>Chat ID</label><input name=chat_id required></div>':'<div class=field><label>SMTP host</label><input name=host required></div><div class=field><label>Port</label><input name=port type=number value=587 required></div><div class=field><label>From email</label><input name=from type=email required></div><div class=field><label>SMTP user (optional)</label><input name=user></div><div class=field><label>SMTP password (optional)</label><input name=pass type=password></div><label><input name=tls type=checkbox> TLS</label><div class=field><label>Recipient email(s), comma-separated</label><input name=recipients required></div>'};fields();kind.onchange=fields;$('#channel').onsubmit=async e=>{e.preventDefault();const f=Object.fromEntries(new FormData(e.target));const config=f.kind==='telegram'?{bot_token:f.bot_token,chat_ids:[f.chat_id]}:{host:f.host,port:Number(f.port),from:f.from,user:f.user||'',pass:f.pass||'',tls:!!e.target.elements.tls.checked,chat_ids:f.recipients.split(',').map(x=>x.trim()).filter(Boolean)};const events=[...e.target.querySelectorAll('input[name=events]:checked')].map(i=>i.value);
if(!events.length){notify('Pick at least one event to notify on');return}
try{await api('/channels',{method:'POST',body:JSON.stringify({name:f.name,kind:f.kind,config,events})});notify('Channel saved · '+events.length+' event'+(events.length===1?'':'s'));render()}catch(x){notify(x.message)}}}
async function runDetail(id){try{const r=await api('/runs/'+id);$('#content').innerHTML=`<div class="card"><h2>Run ${esc(id.slice(0,8))}</h2><div class="grid"><div><span class="muted">Status</span><br><strong>${esc(r.status)}</strong></div><div><span class="muted">Attempts</span><br>${r.attempts}</div><div><span class="muted">Response</span><br>${esc(r.response_excerpt||'—')}</span></div></div><h3>Attempt timeline</h3>${(r.attempts_detail||[]).map(a=>`<p><strong>#${a.attempt_no}</strong> · ${esc(a.status)} · ${esc(a.started_at||'queued')} → ${esc(a.finished_at||'pending')} · ${esc(a.duration_ms??'—')} ms${a.error?` · ${esc(a.error)}`:''}</p>`).join('')||'<p class="muted">No attempts recorded yet.</p>'}</div>`}catch(e){notify(e.message)}}
// Server clock: schedules fire on the server's wall clock, so show that rather
// than the browser's. One sync on load, then a local tick every second.
let srvClock={offset:0,zone:'UTC',tzOffsetMin:0,timer:null};
async function syncEnvLabel(){
  try{
    const r=await fetch('/healthz',{cache:'no-store'});const d=await r.json();
    state.envLabel=d.env==='prod'?'Production':d.env==='dev'?'Development':'Workspace';
    const el=document.querySelector('.crumb-root');
    if(el&&el.textContent!==state.envLabel)el.textContent=state.envLabel;
  }catch{}
}
async function syncServerClock(){
  try{
    const r=await fetch('/time.json',{cache:'no-store'});
    const d=await r.json();
    srvClock.offset=Number(d.unix_ms)-Date.now();
    srvClock.zone=d.zone||'UTC';
    srvClock.tzOffsetMin=Number(d.offset_min||0);
    srvClock.tz=d.tz||srvClock.zone;
    document.querySelectorAll('.srv-at[data-iso]').forEach(el=>{el.textContent=serverClockLabel(el.dataset.iso)});
    const zoneEl=document.getElementById('srv-clock-zone');
    if(zoneEl)zoneEl.textContent=srvClock.zone;
  }catch{}
}
function tickServerClock(){
  // keep the "next run in 4m" countdowns honest while the tab stays open
  document.querySelectorAll('.next-rel[data-iso]').forEach(n=>{
    const t=new Date(n.dataset.iso).getTime();
    if(isNaN(t))return;
    const secs=Math.round((t-(Date.now()+(srvClock.offset||0)))/1000);
    const txt=secs<0?'due now':relShort(secs);
    if(n.textContent!==txt)n.textContent=txt;
  });
  const el=document.getElementById('srv-clock-time');
  if(!el)return;
  const zoneEl=document.getElementById('srv-clock-zone');
  if(zoneEl&&zoneEl.textContent!==srvClock.zone)zoneEl.textContent=srvClock.zone;
  const t=new Date(Date.now()+srvClock.offset+srvClock.tzOffsetMin*60000);
  const p=n=>String(n).padStart(2,'0');
  el.textContent=`${p(t.getUTCHours())}:${p(t.getUTCMinutes())}:${p(t.getUTCSeconds())}`;
}
function startServerClock(){
  if(srvClock.timer)return;
  syncServerClock().then(tickServerClock);
  syncEnvLabel();
  srvClock.timer=setInterval(tickServerClock,1000);
  setInterval(syncServerClock,60000);
}
async function loadBuildVersion(){const el=document.getElementById('build-version');if(!el)return;try{const r=await fetch('/build.json',{cache:'no-store'});const d=await r.json();el.textContent=d.version||'dev'}catch{el.textContent='unknown'}}

const COMMON_TZ=['UTC','Asia/Jakarta','Asia/Shanghai','Asia/Singapore','Asia/Tokyo','Asia/Seoul','Asia/Kolkata','Asia/Dubai','Europe/London','Europe/Berlin','America/New_York','America/Chicago','America/Denver','America/Los_Angeles','America/Sao_Paulo','Australia/Sydney','Pacific/Auckland'];
function tzOptions(){
  let all=[];
  try{ all=Intl.supportedValuesOf('timeZone')||[] }catch{}
  return all.length?Array.from(new Set([...COMMON_TZ,...all])).sort():COMMON_TZ;
}
function wireTimezone(){
  const sel=document.getElementById('tz-select'),btn=document.getElementById('tz-save');
  if(!sel||!btn)return;
  const current=(state.settings&&state.settings['display.timezone'])||srvClock.tz||'Asia/Jakarta';
  const zones=tzOptions();
  if(!zones.includes(current))zones.unshift(current);
  sel.innerHTML=zones.map(z=>`<option value="${esc(z)}"${z===current?' selected':''}>${esc(z)}</option>`).join('');
  const zonePill=document.getElementById('tz-zone');
  if(zonePill)zonePill.textContent=srvClock.zone;
  btn.onclick=async()=>{
    const v=sel.value;btn.disabled=true;
    try{
      const r=await api('/settings/display',{method:'PATCH',body:JSON.stringify({timezone:v})});
      notify(`Display timezone: ${v} (${r.zone}, UTC${r.offset_min>=0?'+':''}${r.offset_min/60})`);
      state.settings=state.settings||{};state.settings['display.timezone']=v;
      await syncServerClock();
      document.querySelectorAll('.srv-at[data-iso]').forEach(el=>{el.textContent=serverClockLabel(el.dataset.iso)});
      await loadPage();
    }catch(e){notify(e.message)}finally{btn.disabled=false}
  };
}
async function settingsAction(){wireTimezone();if($('#toast-duration-select'))$('#toast-duration-select').onchange=e=>persistToastDuration(e.target.value);if($('#toggle-scheduler'))$('#toggle-scheduler').onclick=async()=>{const next=!(state.settings['scheduler.enabled']===true);try{await api('/settings/scheduler',{method:'PATCH',body:JSON.stringify({enabled:next})});notify('Scheduler '+(next?'enabled':'disabled'));state.page='settings';render()}catch(e){notify(e.message)}};document.querySelectorAll('[data-insecure]').forEach(b=>b.onclick=async()=>{try{await api('/security/insecure-http',{method:'PUT',body:JSON.stringify({duration:b.dataset.insecure})});notify('Insecure mode enabled for '+b.dataset.insecure);state.page='settings';render()}catch(e){notify(e.message)}})}
async function governanceAction(){document.querySelectorAll('[data-user-action]').forEach(b=>b.onclick=async()=>{let body; if(b.dataset.userAction==='reset-password'){const p=prompt('New temporary password (10+ chars):');if(!p)return;body={password:p}} if(!confirm(b.dataset.userAction+' this user?'))return;try{await api('/users/'+b.dataset.id+'/'+b.dataset.userAction,{method:'POST',body:JSON.stringify(body||{})});notify('User updated');render()}catch(e){notify(e.message)}});document.querySelectorAll('[data-allow-delete]').forEach(b=>b.onclick=async()=>{if(!confirm('Delete allowlist entry?'))return;try{await api('/allowlist/'+b.dataset.allowDelete,{method:'DELETE'});render()}catch(e){notify(e.message)}});if($('#add-user'))$('#add-user').onclick=()=>{const username=prompt('Username'),password=prompt('Temporary password (10+ chars)'),role=prompt('Role: viewer, qa, admin','viewer');if(username&&password&&role)api('/users',{method:'POST',body:JSON.stringify({username,password,role})}).then(()=>render()).catch(e=>notify(e.message))};if($('#add-allow'))$('#add-allow').onclick=()=>{const pattern=prompt('Hostname or host:port');if(pattern)api('/allowlist',{method:'POST',body:JSON.stringify({pattern})}).then(()=>render()).catch(e=>notify(e.message))};if($('#audit-filter'))$('#audit-filter').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target);state.auditActor=f.get('actor');state.auditAction=f.get('action');state.auditType=f.get('target_type');state.auditOffset=0;render()};if($('#audit-next'))$('#audit-next').onclick=()=>{state.auditOffset=(state.auditOffset||0)+50;render()}}
(async()=>{try{try{const h=await (await fetch('/healthz',{credentials:'same-origin'})).json();state.env=h.env||''}catch{}state.me=await api('/auth/me');if(state.me.must_change_password)changePassword();else render()}catch(e){if(String(e.message).includes('428')){state.me={must_change_password:true};changePassword()}else login()}})();


/* CronSoul console redesign: renderer and interaction layer */
const oldRender=render;
const navItems=[['endpoints','Endpoints','Monitor'],['runs','Runs','Monitor'],['timeline','Timeline','Monitor'],['alerts','Alerts','Monitor'],['channels','Channels','Monitor'],['users','Users','Governance'],['allowlist','Allowlist','Governance'],['audit','Audit log','Governance'],['settings','Settings','System']];
const iconSet={search:'<svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="6"/><path d="m16 16 5 5"/></svg>',menu:'<svg viewBox="0 0 24 24"><path d="M4 7h16M4 12h16M4 17h16"/></svg>',plus:'<svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>',shield:'<svg viewBox="0 0 24 24"><path d="M12 3 4 6v5c0 5 3.4 8.3 8 10 4.6-1.7 8-5 8-10V6z"/><path d="m8 12 2.5 2.5L16 9"/></svg>'};
function statusLabel(v){return {success:'Succeeded',failed:'Failed',failed_after_retries:'Failed after retries',timeout:'Timed out',cancelled:'Cancelled',running:'Running',queued:'Queued',error:'Error'}[String(v||'').toLowerCase()]||String(v||'Unknown')}
function statusClass(v){const x=String(v||'').toLowerCase();return x==='success'?'good':x==='failed'||x==='failed_after_retries'||x==='error'?'bad':x==='timeout'?'warn':'neutral'}
function emptyBlock(title,body,action){return `<div class="empty"><strong>${esc(title)}</strong><p>${esc(body)}</p>${action||''}</div>`}
function badge(v){let x=String(v||'unknown').toLowerCase(),c=x==='success'||x==='enabled'||x==='active'?'good':x==='failed'||x==='error'?'bad':'warn';return `<span class="pill ${c}"><i></i>${esc(v||'Unknown')}</span>`}
function pageDesc(){return {endpoints:'Monitor health, schedules, and delivery performance at a glance.',runs:'Every execution, response, and attempt in one timeline.',timeline:'Where failures cluster across the day, endpoint by endpoint.',alerts:'Investigate missed windows and notification delivery.',channels:'Control where operational signals are delivered.',users:'Manage access to the CronSoul console.',allowlist:'Review destinations permitted for outbound requests.',audit:'A traceable record of governance and security changes.',settings:'Runtime configuration and security posture.'}[state.page]}
function prettyTitle(){return {endpoints:'Endpoints',runs:'Run history',alerts:'Alerts',channels:'Notification channels',users:'Users',allowlist:'Egress allowlist',audit:'Audit log',settings:'Settings',timeline:'Timeline'}[state.page]||'Workspace'}
function redesignedRender(){const visible=navItems.filter(([k])=>!['users','audit'].includes(k)||['admin','superadmin'].includes(state.me?.role)).filter(([k])=>k!=='allowlist'||state.me?.role==='superadmin');let nav='';for(const group of ['Monitor','Governance','System']){const items=visible.filter(x=>x[2]===group);if(items.length)nav+=`<div class="nav-group"><div class="nav-label">${group}</div>${items.map(([k,v])=>`<button class="nav-item ${state.page===k?'active':''}" data-page="${k}" title="${v}">${icons[k]}<span>${v}</span>${k==='alerts'&&state.alerts.length?`<b class="nav-count">${state.alerts.length}</b>`:''}</button>`).join('')}</div>`}const act=state.page==='endpoints'?`<button class="primary" id="new">${iconSet.plus} New endpoint</button>`:state.page==='channels'?`<button class="primary" id="add-channel">${iconSet.plus} Add channel</button>`:state.page==='users'?`<button class="primary" id="add-user">${iconSet.plus} Add user</button>`:state.page==='allowlist'?`<button class="primary" id="add-allow">${iconSet.plus} Add pattern</button>`:'';app.innerHTML=`<div class="shell"><aside class="side" id="side"><div class="brand"><img src="/ui/cronsoul-mark.svg" alt="CronSoul"><span>CronSoul<span class="brand-dot">.</span></span></div><div class="env-badge" title="One CronSoul instance serves exactly one environment. Deploy a separate instance per environment."><i></i><span class="env-name">${esc((state.env||'dev'))}</span><span class="env-kind">environment</span></div><nav>${nav}</nav><div class="side-foot">${iconSet.shield}<div><strong>Protected environment</strong><span>Outbound policy enforced</span></div></div><div class="side-credit">Built by <b>user4xn</b></div><div class="profile"><b>${esc((state.me?.username||'?')[0].toUpperCase())}</b><div><strong>${esc(state.me?.username||'Operator')}</strong><small>${esc(state.me?.role||'viewer')}</small></div><button class="icon-btn signout" id="logout" title="Sign out" aria-label="Sign out"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M9 6H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h3"/><path d="M16 12H9"/><path d="m13 8 4 4-4 4"/></svg></button></div></aside><div class="drawer-scrim" id="scrim"></div><main class="main"><header class="topbar"><button class="icon-btn mobile-toggle" id="menu" aria-label="Open navigation" aria-expanded="false">${iconSet.menu}</button><div class="crumb"><span class="crumb-root">${esc(state.envLabel||"Workspace")}</span><span class="crumb-sep" aria-hidden="true">/</span><strong>${prettyTitle()}</strong></div><div class="top-actions"><div class="srv-clock" id="srv-clock" title="Server time — schedules are evaluated in this clock"><i></i><span id="srv-clock-time">--:--:--</span><small id="srv-clock-zone">UTC</small></div><button class="command" id="command">${iconSet.search}<span>Search anything</span><kbd>⌘ K</kbd></button><button class="icon-btn" id="refresh" title="Refresh" aria-label="Refresh">↻</button></div></header><div class="page-head"><div><div class="eyebrow">${state.page==='endpoints'?'LIVE OPERATIONS':'WORKSPACE'}</div><h1>${prettyTitle()}</h1><p class="muted">${pageDesc()}</p></div>${act}</div><section id="content"><div class="loading">Loading live data…</div></section></main></div>`;document.querySelectorAll('[data-page]').forEach(b=>b.onclick=()=>{state.page=b.dataset.page;closeDrawer();redesignedRender()});const closeDrawer=()=>{$('#side').classList.remove('open');$('#scrim').classList.remove('open');$('#menu').setAttribute('aria-expanded','false')};var closeNav=()=>{$('#side').classList.remove('open');const sc=$('#scrim');if(sc)sc.classList.remove('open');const mb=$('#menu');if(mb)mb.setAttribute('aria-expanded','false')};$('#menu').onclick=()=>{const open=$('#side').classList.toggle('open');const sc=$('#scrim');if(sc)sc.classList.toggle('open',open);$('#menu').setAttribute('aria-expanded',open)};if($('#logout'))$('#logout').onclick=async()=>{try{await api('/auth/logout',{method:'POST'})}catch(e){}state.me=null;login()};if($('#scrim'))$('#scrim').onclick=closeNav;document.addEventListener('keydown',e=>{if(e.key==='Escape'&&!document.querySelector('.modal-backdrop'))closeNav()});$('#scrim').onclick=closeDrawer;$('#refresh').onclick=()=>{const b=$('#refresh');b.classList.add('spinning');loadPage().finally(()=>b.classList.remove('spinning'))};$('#command').onclick=()=>($('#job-search')||$('#run-search'))?.focus();if($('#new'))$('#new').onclick=()=>endpointForm();loadPage()}
render=redesignedRender;
const oldDraw=draw;
function redesignedDraw(){const c=$('#content');if(state.page==='endpoints'){endpointsView(c)}else if(state.page==='runs'){runsView(c)}else if(state.page==='timeline'){timelineView(c)}else if(state.page==='audit'){auditView(c)}else if(state.page==='allowlist'){allowView(c)}else oldDraw();bindRedesign()}
function bindRedesign(){startServerClock();loadBuildVersion();document.querySelectorAll('[data-ch-test]').forEach(b=>b.onclick=()=>channelTest(b.dataset.chTest));
document.querySelectorAll('[data-ch-edit]').forEach(b=>b.onclick=()=>channelModal((state.channels||[]).find(x=>x.id===b.dataset.chEdit)));
document.querySelectorAll('[data-ch-del]').forEach(b=>b.onclick=()=>channelDelete(b.dataset.chDel));
const sh=$('[data-sort]');if(sh){const flip=()=>{state.sortDir=state.sortDir==='asc'?'desc':'asc';redesignedDraw()};sh.onclick=flip;sh.onkeydown=e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();flip()}}}document.querySelectorAll('[data-preview]').forEach(b=>b.onclick=async()=>{b.disabled=true;try{const r=await api('/jobs/'+b.dataset.preview+'/preview',{method:'POST',body:'{}'});notify(r.ok?`Reachable · HTTP ${r.status} · ${r.duration_ms} ms`:`Reached, but rejected · HTTP ${r.status} · ${r.duration_ms} ms`)}catch(e){notify(e.message)}finally{b.disabled=false}});document.querySelectorAll('[data-play]').forEach(b=>b.onclick=()=>play(b.dataset.play));document.querySelectorAll('[data-del-job]').forEach(b=>b.onclick=()=>armDeleteJob(b));document.querySelectorAll('[data-edit]').forEach(b=>b.onclick=()=>{const j=state.jobs.find(x=>x.id===b.dataset.edit);if(j)endpointForm(j)});document.querySelectorAll('[data-run]').forEach(b=>b.onclick=()=>runDetail(b.dataset.run));if($('#add-channel'))$('#add-channel').onclick=()=>channelModal();if($('#add-channel-empty'))$('#add-channel-empty').onclick=()=>channelModal();if($('#add-user'))$('#add-user').onclick=()=>modalUser();if($('#add-allow'))$('#add-allow').onclick=()=>modalAllow();document.querySelectorAll('[data-user-action]').forEach(b=>b.onclick=()=>userActionRedesign(b));document.querySelectorAll('[data-allow-delete]').forEach(b=>b.onclick=()=>deleteAllowRedesign(b))}



function patternKind(p){const v=String(p||'');
  if(/^\*\./.test(v))return['wildcard','bad'];
  if(/^\[/.test(v)||/^\d+\.\d+\.\d+\.\d+/.test(v))return['IP','warn'];
  if(/:\d+$/.test(v))return['host:port','neutral'];
  return['host','good']}
function allowView(c){
  const all=state.allowlist||[],q=String(state.allowQ||'').toLowerCase();
  const rows=all.filter(x=>!q||[x.pattern,x.note].some(v=>String(v||'').toLowerCase().includes(q)));
  const wild=all.filter(x=>/^\*\./.test(x.pattern)).length;
  const withPort=all.filter(x=>/:\d+$/.test(x.pattern)||/^\[/.test(x.pattern)).length;
  const noted=all.filter(x=>x.note&&String(x.note).trim()).length;
  c.innerHTML=`<div class="kpi-grid">${kpi('Allowed hosts',all.length,rows.length!==all.length?rows.length+' match the filter':'Every entry active','cyan')}${kpi('Wildcards',wild,wild?'Broad trust — review these':'None, all exact hosts',wild?'red':'green')}${kpi('Host + port',withPort,'Pinned to a single port','blue')}${kpi('Documented',noted+'/'+all.length,'Entries carrying a note',noted===all.length?'green':'red')}</div>
  ${all.length?'':`<div class="banner warn"><strong>The allowlist is empty, so every run is refused.</strong> CronSoul only calls hosts listed here. Add the host of each endpoint (for example <code>api.example.com</code>, or <code>api.example.com:8443</code> for a non-standard port) before pressing Play. The endpoint form also offers to add a missing host.</div>`}
  <div class="toolbar"><div class="searchbox">${iconSet.search}<input id="allow-search" placeholder="Filter by host or note…" value="${esc(state.allowQ||'')}"><kbd>/</kbd></div><span class="live-dot">● Policy enforced on every run</span></div>
  <div class="table-card"><div class="table-title"><div><h2>Egress allowlist</h2><span class="muted">A run is refused unless its host matches one of these entries.</span></div></div>
  <div class="table-wrap"><table class="table allow-table"><thead><tr><th>Pattern</th><th>Type</th><th>Note</th><th>Added</th><th class="col-actions">Actions</th></tr></thead><tbody>
  ${rows.slice().sort((a,b)=>String(a.pattern).localeCompare(String(b.pattern))).map(x=>{const [kind,tone]=patternKind(x.pattern);return `<tr>
    <td data-label="Pattern"><span class="mono">${esc(x.pattern)}</span></td>
    <td data-label="Type"><span class="pill ${tone}"><i></i>${kind}</span></td>
    <td data-label="Note">${x.note?esc(x.note):'<span class="dim">—</span>'}</td>
    <td data-label="Added"><strong>${relTime(x.created_at)}</strong><small>${stamp(x.created_at)}</small></td>
    <td class="cell-actions" data-label="Actions"><button class="row-action danger" data-allow-del="${x.id}" title="Delete ${esc(x.pattern)}">Delete</button></td></tr>`}).join('')||`<tr><td colspan="5" class="empty">${all.length?'No entry matches this filter.':'No destination is permitted yet — every run will be refused. Add the hosts your jobs call.'}</td></tr>`}
  </tbody></table></div></div>`;
  const si=document.getElementById('allow-search');
  if(si){let t;si.oninput=e=>{clearTimeout(t);const v=e.target.value;const pos=e.target.selectionStart;t=setTimeout(()=>{state.allowQ=v;keepFocus(()=>allowView(c));const el=document.getElementById('allow-search');if(el){el.focus();try{el.setSelectionRange(pos,pos)}catch{}}},200)}}
  document.querySelectorAll('[data-allow-del]').forEach(b=>b.onclick=()=>{
    if(b.dataset.armed){deletePattern(b.dataset.allowDel);return}
    b.dataset.armed='1';b.textContent='Confirm';b.classList.add('armed');
    setTimeout(()=>{if(b.dataset.armed){delete b.dataset.armed;b.textContent='Delete';b.classList.remove('armed')}},4000);
  });
}
async function deletePattern(id){
  try{await api('/allowlist/'+id,{method:'DELETE'});notify('Pattern removed');state.page='allowlist';await loadPage();redesignedRender()}
  catch(e){notify(e.message)}
}


const channelIcons={
  smtp:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><rect x="2.5" y="5" width="19" height="14" rx="2.5"/><path d="m3.5 7 8.5 6 8.5-6"/></svg>',
  telegram:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="M21.5 4.3 2.9 11.2c-.9.3-.9 1.5 0 1.8l4.4 1.4 1.7 5.1c.3.9 1.4 1.1 2 .4l2.4-2.6 4.1 3c.7.5 1.7.1 1.9-.7l3-13.9c.2-.9-.7-1.6-1.5-1.3Z"/><path d="m7.3 14.4 11-7.2-8.6 8.1-.3 3"/></svg>',
  webhook:'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="M12 3v6m0 6v6M3.5 7.5 8.7 10m6.6 4 5.2 2.5M3.5 16.5 8.7 14m6.6-4 5.2-2.5"/><circle cx="12" cy="12" r="3"/></svg>'};

function channelView(){
  const list=state.channels||[];
  if(!list.length){
    return emptyBlock('No delivery channels','Without a channel, failures and missed runs stay inside this console. Add SMTP email or a Telegram bot to push them out.','<button class="primary" id="add-channel-empty">Add your first channel</button>');
  }
  return `<div class="ch-list">${list.map(c=>{
    const meta=c.meta||{};
    const targets=(meta.targets||[]);
    const where=c.kind==='smtp'
      ? (meta.host?`${meta.host}${meta.port?':'+meta.port:''}`:'SMTP')+(meta.from?` · from ${meta.from}`:'')
      : (targets.length?targets.join(', '):'Telegram bot');
    return `<article class="ch-card">
      <div class="ch-icon ${esc(c.kind)}">${channelIcons[c.kind]||channelIcons.webhook}</div>
      <div class="ch-body">
        <div class="ch-top"><h3>${esc(c.name)}</h3><span class="pill ${c.enabled?'good':'warn'}"><i></i>${c.enabled?'Active':'Paused'}</span><span class="ch-kind">${esc(c.kind)}</span></div>
        <p class="ch-where mono">${esc(where)}</p>
        <div class="ch-events">${(c.events||[]).map(e=>`<span class="ev-tag">${esc(e)}</span>`).join('')||'<span class="dim">all events</span>'}</div>
      </div>
      <div class="ch-actions">
        <span class="ch-result" id="ch-res-${esc(c.id)}"></span>
        <button class="ghost" data-ch-test="${esc(c.id)}">Send test</button>
        <button class="ghost" data-ch-edit="${esc(c.id)}">Edit</button>
        <button class="ghost danger" data-ch-del="${esc(c.id)}">Delete</button>
      </div>
    </article>`}).join('')}</div>`;
}

function alertsView(){
  const alerts=state.alerts||[],outbox=state.outbox||[];
  const open=alerts.filter(a=>!a.resolved_at).length;
  const pending=outbox.filter(o=>o.status==='pending').length;
  const failed=outbox.filter(o=>o.status==='failed').length;
  return `<div class="kpi-grid">${kpi('Open alerts',open,open?'Needs attention':'Nothing outstanding',open?'red':'green')}${kpi('Missed windows',alerts.length,'Scheduled runs that never fired','blue')}${kpi('Queued notifications',pending,'Waiting for delivery','cyan')}${kpi('Failed deliveries',failed,failed?'Check the channel config':'Outbox healthy',failed?'red':'green')}</div>
  <div class="grid-two">
    <div class="table-card"><div class="table-title"><div><h2>Missed runs</h2><span class="muted">A scheduled window passed without a matching run.</span></div></div>
      <div class="table-wrap"><table class="table"><thead><tr><th>Endpoint</th><th>Window</th><th>State</th></tr></thead><tbody>
      ${alerts.map(a=>{const job=(state.jobs||[]).find(j=>j.id===a.job_id);return `<tr><td data-label="Endpoint"><strong>${esc(job?job.name:String(a.job_id).slice(0,8))}</strong><small>${esc(job?job.key:'')}</small></td><td data-label="Window" class="mono">${stamp(a.window_start)}</td><td data-label="State">${a.resolved_at?'<span class="pill good"><i></i>resolved</span>':'<span class="pill bad"><i></i>open</span>'}</td></tr>`}).join('')||`<tr><td colspan="3" class="empty">Nothing missed. Every scheduled window produced a run.</td></tr>`}
      </tbody></table></div></div>
    <div class="table-card"><div class="table-title"><div><h2>Notification outbox</h2><span class="muted">Retries run at 10s, 60s and 300s before a delivery is marked failed.</span></div></div>
      <div class="table-wrap"><table class="table"><thead><tr><th>Event</th><th>Channel</th><th>State</th><th>Attempts</th><th>Next retry</th></tr></thead><tbody>
      ${outbox.map(o=>`<tr><td data-label="Event"><strong>${esc(o.kind)}</strong><small>${esc(String(o.job_id||'').slice(0,8))}</small></td><td data-label="Channel">${esc(o.channel)}</td><td data-label="State"><span class="pill ${o.status==='sent'?'good':o.status==='failed'?'bad':'warn'}"><i></i>${esc(o.status)}</span></td><td data-label="Attempts" class="num mono">${esc(o.attempts)}</td><td data-label="Next retry">${o.status==='pending'?relTime(o.next_attempt_at):'<span class="dim">—</span>'}</td></tr>`).join('')||`<tr><td colspan="5" class="empty">Outbox is clear. Nothing is waiting to be delivered.</td></tr>`}
      </tbody></table></div></div>
  </div>`;
}

function channelModal(channel){
  const c=channel||{};
  const meta=c.meta||{};
  const isEdit=!!channel;
  const events=new Set((c.events&&c.events.length?c.events:['failure','recovered','missed_run','assertion_failed']));
  const evList=[["failure","A run fails after all retries","The endpoint finally gives up"],["recovered","A run succeeds after earlier failures","The endpoint came back"],["missed_run","A scheduled window was missed","The scheduler did not fire"],["assertion_failed","An assertion fails","QA checks on the response break"],["manual_run","A manual run (Play) succeeds","Confirms a run someone started by hand; failures already arrive as “run fails”"]];
  const x=modalBase(isEdit?`Edit channel · ${esc(c.name)}`:'Add delivery channel', `
    <form id="mform" novalidate>
      <div class="form-grid-2">
        <label>Name<input name="name" value="${esc(c.name||'')}" placeholder="Ops on-call"></label>
        <label>Provider<select name="kind" ${isEdit?'disabled':''}><option value="smtp"${c.kind==='smtp'?' selected':''}>Email (SMTP)</option><option value="telegram"${c.kind==='telegram'?' selected':''}>Telegram bot</option></select></label>
      </div>
      <div id="ch-fields"></div>
      <fieldset class="ev-field"><legend>Notify me when</legend>
        ${evList.map(([v,t,h])=>`<button type="button" class="ev-opt" data-ev="${v}" aria-pressed="${events.has(v)}"><span class="ev-tick" aria-hidden="true"></span><span class="ev-copy"><b>${t}</b><small>${h}</small></span></button>`).join('')}
      </fieldset>
      <div class="actions"><button type="button" class="ghost" id="ch-cancel">Cancel</button><button class="primary" type="submit">${isEdit?'Save changes':'Create channel'}</button></div>
    </form>`);
  const form=x.querySelector('form'),submit=form.querySelector('button[type=submit]');
  submit.addEventListener('click',e=>{e.preventDefault();form.requestSubmit()});
  form.querySelectorAll('.ev-opt').forEach(btn=>btn.addEventListener('click',()=>{
    btn.setAttribute('aria-pressed', btn.getAttribute('aria-pressed')==='true'?'false':'true');
  }));
  x.querySelector('#ch-cancel').onclick=()=>document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());
  const kindSel=form.querySelector('[name=kind]'),fields=x.querySelector('#ch-fields');
  const targets=(meta.targets||[]).join(', ');
  const paint=()=>{
    const k=kindSel.value;
    fields.innerHTML = k==='smtp'
      ? `<div class="form-grid-2"><label>SMTP host<input name="host" value="${esc(meta.host||'')}" placeholder="smtp.example.com"></label>
         <label>Port<input name="port" type="number" value="${meta.port||587}" placeholder="587"></label></div>
         <div class="form-grid-2"><label>From address<input name="from" type="email" value="${esc(meta.from||'')}" placeholder="cronsoul@example.com"></label>
         <label>SMTP user<input name="user" placeholder="optional"></label></div>
         <div class="form-grid-2"><label>SMTP password<input name="pass" type="password" placeholder="${isEdit?'leave empty to keep':'optional'}"></label>
         <label>Recipients<input name="recipients" value="${esc(targets)}" placeholder="ops@example.com, oncall@example.com"></label></div>
         <label class="check"><input type="checkbox" name="tls"${meta.tls===false?'':' checked'}><span>Use TLS (STARTTLS)</span></label>`
      : `<div class="form-grid-2"><label>Bot token<input name="bot_token" type="password" placeholder="${isEdit?'leave empty to keep':'123456:ABC-DEF…'}"></label>
         <label>Chat IDs<input name="chat_id" value="${esc(targets)}" placeholder="-1001234567890"></label></div>`;
  };
  paint();
  kindSel.onchange=paint;
  form.onsubmit=async e=>{
    e.preventDefault();modalError('');
    const data=Object.fromEntries(new FormData(form));
    const kind=isEdit?c.kind:data.kind;
    const events=[...form.querySelectorAll('.ev-opt[aria-pressed=true]')].map(b=>b.dataset.ev);
    if(!events.length){modalError('Pick at least one event to notify on.');return}
    if(!String(data.name||'').trim()){modalError('Give the channel a name.');return}
    let config;
    if(kind==='smtp'){
      const recipients=String(data.recipients||'').split(',').map(v=>v.trim()).filter(Boolean);
      if(!isEdit&&(!data.host||!data.from||!recipients.length)){modalError('Host, from address and at least one recipient are required.');return}
      config={host:data.host||'',port:Number(data.port||0),from:data.from||'',user:data.user||'',pass:data.pass||'',tls:!!data.tls,chat_ids:recipients};
    }else{
      const chats=String(data.chat_id||'').split(',').map(v=>v.trim()).filter(Boolean);
      if(!isEdit&&(!data.bot_token||!chats.length)){modalError('Bot token and at least one chat ID are required.');return}
      config={bot_token:data.bot_token||'',chat_ids:chats};
    }
    modalBusy(submit,true);
    try{
      await api(isEdit?'/channels/'+c.id:'/channels',{method:isEdit?'PUT':'POST',body:JSON.stringify({name:data.name,kind,config,events})});
      document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());
      notify(isEdit?'Channel updated':'Channel created');
      state.channels=await api('/channels');redesignedRender();
    }catch(err){modalError(err.message)}finally{modalBusy(submit,false,isEdit?'Save changes':'Create channel')}
  };
}

async function channelTest(id){
  const res=document.getElementById('ch-res-'+id);
  if(res){res.className='ch-result';res.textContent='Sending…'}
  try{
    const r=await api('/channels/'+id+'/test',{method:'POST'});
    const msg=r.sent?`Delivered to ${r.delivered} of ${r.total} target${r.total===1?'':'s'}`:(r.reason||r.error||'Delivery failed');
    if(res){res.className='ch-result '+(r.sent?'ok':'bad');res.textContent=msg}
    notify(r.sent?'Test notification delivered':msg);
  }catch(e){if(res){res.className='ch-result bad';res.textContent=e.message}notify(e.message)}
}

async function channelDelete(id){
  if(!confirm('Delete this channel? Queued notifications for it are dropped.'))return;
  try{await api('/channels/'+id,{method:'DELETE'});notify('Channel deleted');state.channels=await api('/channels');redesignedRender()}catch(e){notify(e.message)}
}

function actionTone(action){const a=String(action||'').toLowerCase();
  if(a.startsWith('allowlist'))return 'warn';
  if(a.startsWith('user'))return 'bad';
  if(a.startsWith('settings')||a.startsWith('security'))return 'neutral';
  if(a.startsWith('job'))return 'good';
  return 'neutral'}
function auditView(c){
  const rows=state.audit||[],total=state.auditTotal??rows.length;
  const actions=[...new Set(rows.map(r=>r.action))].sort();
  const types=[...new Set(rows.map(r=>r.target_type).filter(Boolean))].sort();
  const actors=[...new Set(rows.map(r=>r.actor_name))].sort();
  const security=rows.filter(r=>/^(settings|security|allowlist)\./.test(String(r.action))).length;
  const last=rows[0]?.at;
  c.innerHTML=`<div class="kpi-grid">${kpi('Events',rows.length,total>rows.length?`${total} total matched`:'all loaded','cyan')}${kpi('Actors',actors.length,actors.slice(0,3).join(', ')||'—','blue')}${kpi('Security changes',security,'Allowlist, settings, users','red')}${kpi('Latest',last?relTime(last):'—',last?stamp(last):'No activity yet','green')}</div>
  <div class="toolbar"><div class="searchbox">${iconSet.search}<input id="audit-search" placeholder="Search action, target, actor…" value="${esc(state.auditQ||'')}"><kbd>/</kbd></div>
    <div class="filters">
      <select id="audit-action"><option value="">All actions</option>${actions.map(a=>`<option value="${esc(a)}"${state.auditAction===a?' selected':''}>${esc(a)}</option>`).join('')}</select>
      <select id="audit-type"><option value="">All targets</option>${types.map(t=>`<option value="${esc(t)}"${state.auditType===t?' selected':''}>${esc(t)}</option>`).join('')}</select>
      ${state.auditQ||state.auditAction||state.auditType||state.auditActor?'<button class="ghost" id="audit-reset">Reset</button>':''}
    </div></div>
  <div class="table-card"><div class="table-title"><div><h2>Recorded changes</h2><span class="muted">${rows.length} of ${total} · newest first</span></div></div>
  <div class="table-wrap"><table class="table audit-table"><thead><tr><th>When</th><th>Actor</th><th>Action</th><th>Target</th><th>Source</th></tr></thead><tbody>
  ${rows.map(r=>`<tr><td data-label="When"><strong>${relTime(r.at)}</strong><small>${stamp(r.at)}</small></td>
    <td data-label="Actor"><span class="who" title="${esc(r.actor_name)}"><b>${esc((r.actor_name||'?')[0].toUpperCase())}</b>${esc(r.actor_name)}</span></td>
    <td data-label="Action"><span class="pill ${actionTone(r.action)}"><i></i>${esc(r.action)}</span></td>
    <td data-label="Target">${esc(r.target_type||'—')}${r.target_id?`<small class="mono dim ellipsis" title="${esc(r.target_id)}">${esc(String(r.target_id).slice(0,18))}</small>`:''}</td>
    <td data-label="Source" class="mono dim">${esc(r.ip||'—')}</td></tr>`).join('')||`<tr><td colspan="5" class="empty">No audit entries match this filter.</td></tr>`}
  </tbody></table></div>
  ${rows.length<total?`<div class="table-foot"><button class="ghost" id="audit-more">Load ${Math.min(100,total-rows.length)} more</button></div>`:''}</div>`;
  const s1=document.getElementById('audit-search');
  if(s1){let t;s1.oninput=e=>{clearTimeout(t);const v=e.target.value;const pos=e.target.selectionStart;t=setTimeout(async()=>{state.auditQ=v;state.auditOffset=0;await loadPage();const el=document.getElementById('audit-search');if(el){el.focus();try{el.setSelectionRange(pos,pos)}catch{}}},300)}}
  const s2=document.getElementById('audit-action');
  if(s2)s2.onchange=e=>{state.auditAction=e.target.value;state.auditOffset=0;loadPage()};
  const s3=document.getElementById('audit-type');
  if(s3)s3.onchange=e=>{state.auditType=e.target.value;state.auditOffset=0;loadPage()};
  const rs=document.getElementById('audit-reset');
  if(rs)rs.onclick=()=>{state.auditQ='';state.auditAction='';state.auditType='';state.auditActor='';state.auditOffset=0;loadPage()};
  const more=document.getElementById('audit-more');
  if(more)more.onclick=async()=>{more.disabled=true;more.textContent='Loading…';state.auditOffset=(state.auditOffset||0)+100;const q=new URLSearchParams({actor:state.auditActor||'',action:state.auditAction||'',target_type:state.auditType||'',q:state.auditQ||'',limit:'100',offset:String(state.auditOffset)});const d=await api('/audit?'+q);state.audit=[...(state.audit||[]),...(d.data||[])];state.auditTotal=d.total??state.auditTotal;redesignedDraw()};
}


function armDeleteJob(b){
  if(b.dataset.armed){
    delete b.dataset.armed;b.classList.remove('armed');b.textContent='✕';
    deleteJob(b.dataset.delJob);return;
  }
  b.dataset.armed='1';b.classList.add('armed');b.textContent='Confirm';
  setTimeout(()=>{if(b.dataset.armed){delete b.dataset.armed;b.classList.remove('armed');b.textContent='✕'}},4000);
}
async function deleteJob(id){
  const j=state.jobs.find(x=>x.id===id)||{};
  try{
    await api('/jobs/'+id,{method:'DELETE'});
    notify('Deleted '+(j.name||'endpoint')+' · run history kept');
    await loadPage();redesignedRender();
  }catch(e){notify(e.message)}
}

// Re-rendering replaces the input node, which drops focus and the caret.
// Every filter re-render goes through here so typing stays uninterrupted.
function keepFocus(fn){
  const a=document.activeElement;
  const id=a&&a.id;
  const start=a&&typeof a.selectionStart==='number'?a.selectionStart:null;
  fn();
  if(!id)return;
  const el=document.getElementById(id);
  if(!el)return;
  el.focus();
  if(start!==null&&typeof el.setSelectionRange==='function'){
    const end=a&&typeof a.selectionEnd==='number'?a.selectionEnd:start;
    try{el.setSelectionRange(start,end)}catch{}
  }
}
function relTime(iso){if(!iso)return '—';const t=new Date(iso).getTime();if(isNaN(t))return esc(iso);const diff=Math.round(((Date.now()+(srvClock.offset||0))-t)/1000);const a=Math.abs(diff);const unit=a<60?a+'s':a<3600?Math.round(a/60)+'m':a<86400?Math.round(a/3600)+'h':Math.round(a/86400)+'d';return diff>=0?unit+' ago':'in '+unit}
function stamp(iso){
  if(!iso)return '—';
  const t=new Date(iso).getTime();
  if(isNaN(t))return esc(iso);
  const d=new Date(t+srvClock.tzOffsetMin*60000);
  const p=n=>String(n).padStart(2,'0');
  return `${d.getUTCFullYear()}-${p(d.getUTCMonth()+1)}-${p(d.getUTCDate())} ${p(d.getUTCHours())}:${p(d.getUTCMinutes())} ${srvClock.zone}`;
}
function runStatus(r){return badge(statusLabel(r.status))}

// Runs refused before any HTTP exchange (egress policy, bad auth config) have
// no status code; the reason lives in run.error and must be visible.
function runReason(err){
  const e=String(err||'').trim();if(!e)return '';
  const allow=/not on allowlist|tidak ada di allowlist/i.test(e);
  const canAllow=state.me?.role==='superadmin';
  return `<small class="run-reason" title="${esc(e)}">${esc(allow?'Blocked: host not on allowlist':e)}${allow&&canAllow?' · <a href="#" data-goto-allowlist>Allowlist</a>':''}</small>`;
}
function bindGotoAllowlist(root){
  (root||document).querySelectorAll('[data-goto-allowlist]').forEach(a=>a.onclick=e=>{e.preventDefault();e.stopPropagation();document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());state.page='allowlist';redesignedRender()});
}

// Next scheduled runs: the half of a cron console that history cannot show.
function upcomingSection(){
  const u=state.upcoming;
  if(!u)return '';
  const rows=u.data||[];
  const off=u.scheduler_enabled===false;
  const next=rows[0];
  return `<div class="table-card upcoming">
    <div class="table-title"><div><h2>Next 60 minutes</h2><span class="muted">${off?'Scheduler is disabled — nothing will fire until it is enabled in Settings.':rows.length+' scheduled run'+(rows.length===1?'':'s')+' queued by cron'}</span></div>
    <span class="pill ${off?'warn':rows.length?'good':'neutral'}"><i></i>${off?'Scheduler off':rows.length?(next?'first '+relShort(next.in_seconds):'scheduled'):'nothing due'}</span></div>
    ${rows.length?`<div class="table-wrap"><table class="table upcoming-table"><thead><tr><th>Endpoint</th><th>Runs at</th><th>In</th></tr></thead><tbody>
      ${rows.map(r=>`<tr><td data-label="Endpoint"><strong>${esc(r.job_name||r.job_key)}</strong><small class="mono dim">${esc(r.job_key)} · ${esc(r.job_method||'')}</small></td>
        <td data-label="Runs at"><strong class="mono srv-at" data-iso="${esc(r.at)}">${serverClockLabel(r.at)}</strong><small>${stamp(r.at)}</small></td>
        <td data-label="In" class="num"><span class="tag">${relShort(r.in_seconds)}</span></td></tr>`).join('')}
      </tbody></table></div>`:`<div class="empty">${off?'Enable the scheduler to see upcoming runs.':'No endpoint is scheduled to run in the next hour. Schedules may be hourly or less frequent.'}</div>`}
    </div>`;
}
// Render an RFC3339 instant in the server's wall clock, matching the top-bar clock.
function serverClockLabel(iso){
  const t=new Date(iso).getTime();
  if(isNaN(t))return '—';
  const d=new Date(t+srvClock.tzOffsetMin*60000);
  const p=n=>String(n).padStart(2,'0');
  return `${p(d.getUTCHours())}:${p(d.getUTCMinutes())} ${srvClock.zone}`;
}
function relShort(sec){
  const s=Math.max(0,Math.round(sec));
  if(s<60)return 'in '+s+'s';
  const m=Math.floor(s/60);
  if(m<60)return 'in '+m+'m';
  return 'in '+Math.floor(m/60)+'h '+String(m%60).padStart(2,'0')+'m';
}

// "Next run in 4m" per endpoint, in the server clock the scheduler itself uses.
// Countdowns are refreshed in place by tickServerClock so an open tab never
// shows a stale "in 4m". Every state is spelled out instead of going blank:
// paused endpoint, missing cursor (invalid/unschedulable cron), unreadable
// timestamp, and a cursor already in the past.
function nextRunCell(j){
  if(j&&j.enabled===false)return '<span class="dim" title="Endpoint is paused — it will not fire">paused</span>';
  if(!j||!j.next_run_at)return '<span class="dim" title="No upcoming run — the cron expression may be invalid or unschedulable">—</span>';
  const iso=j.next_run_at,t=new Date(iso).getTime();
  if(isNaN(t))return `<span class="dim" title="Unreadable timestamp: ${esc(iso)}">unknown</span>`;
  const secs=Math.round((t-(Date.now()+(srvClock.offset||0)))/1000);
  const at=`<small class="mono srv-at" data-iso="${esc(iso)}">${serverClockLabel(iso)}</small>`;
  if(secs<0)return `<strong class="dim" title="The scheduled instant has passed and the cursor has not advanced">due now</strong>${at}`;
  return `<strong class="mono next-rel" data-iso="${esc(iso)}" title="Next run ${esc(stamp(iso))}">${relShort(secs)}</strong>${at}`;
}

const PAGE_SIZE=25;
function cronProse(expr){
  const d=describeCron(expr);
  if(d.bad)return expr;
  return d.text.replace(/^Runs /,'').replace(/,\s*every day\.?$/,'').replace(/\.$/,'');
}
function scheduleWords(j){return j&&j.cron?esc(cronProse(j.cron)):''}
function retryWords(j){
  const max=j.retry&&j.retry.max||0;
  if(!max)return '<span class="dim">0×</span>';
  const kind=j.retry.backoff==='fixed'?'fixed':'exp';
  return `<span class="mono">${max}× ${kind}</span>`;
}
function lastRunCell(j){
  if(!j.last_run_status)return '<span class="run-dot never"></span><span class="never-txt">never run</span>';
  const st=String(j.last_run_status).toLowerCase();
  const tone=st==='success'?'ok':(st==='timeout'?'warn':'bad');
  return `<span class="run-dot ${tone}"></span><span class="run-line">${esc(statusLabel(j.last_run_status))} · ${relTime(j.last_run_at)}</span>${st!=='success'?runReason(j.last_run_error):''}`;
}
const TIER_INFO={
  A:['Read-only','Safe to call any time; only reads data. Default for GET/HEAD.'],
  B:['Writes data','Changes state in your service. Default for POST/PUT/PATCH/DELETE.'],
  C:['High impact','Money, messages to customers, or irreversible changes. Run with care.']
};
const TIER_NOTE='Tier is a label for triage and filtering; it does not change retries, roles or confirmations.';
function tierBadge(t){
  const k=String(t||'A').toUpperCase(),[name,desc]=TIER_INFO[k]||['',''];
  return `<span class="tier ${esc(k)}" title="Tier ${esc(k)} · ${esc(name)} — ${esc(desc)} ${TIER_NOTE}">${esc(k)}</span>`;
}
function endpointMatches(j,f){
  if(f==='all')return true;
  if(f==='clock')return !!j.clock_aware;
  if(f==='failed')return ['failed','failed_after_retries','timeout','error'].includes(String(j.last_run_status||'').toLowerCase());
  if(f.startsWith('tier-'))return String(j.risk_tier||'A').toUpperCase()===f.slice(5);
  return true;
}
function endpointsView(c){
  const all=state.jobs||[],st=state.stats||{};
  const q=String(state.query||'').toLowerCase(),f=state.epFilter||'all';
  const filtered=all.filter(j=>endpointMatches(j,f)&&(!q||[j.name,j.key,j.service,j.url].some(v=>String(v||'').toLowerCase().includes(q))));
  const pages=Math.max(1,Math.ceil(filtered.length/PAGE_SIZE));
  const page=Math.min(state.epPage||1,pages);
  const rows=filtered.slice((page-1)*PAGE_SIZE,page*PAGE_SIZE);
  const tierCount=t=>all.filter(j=>String(j.risk_tier||'A').toUpperCase()===t).length;
  const chips=[['all',`All ${all.length}`],['tier-A',`Tier A ${tierCount('A')}`],['tier-B',`Tier B ${tierCount('B')}`],['tier-C',`Tier C ${tierCount('C')}`],['clock',`Clock-aware ${all.filter(j=>j.clock_aware).length}`],['failed',`Last failed ${all.filter(j=>endpointMatches(j,'failed')).length}`]].filter(([k,label])=>k==='all'||!/(\s0)$/.test(label));
  const rate=(st.runs_24h??0)>0?Number(st.success_rate||0).toFixed(1)+'%':'—';
  const delta=(st.runs_24h_prev??0)>0?Number(st.success_rate||0)-Number(st.success_rate_prev||0):null;
  const deltaTxt=delta==null?'no prior window':`${delta>=0?'+':''}${delta.toFixed(1)}% vs yesterday`;
  c.innerHTML=`<div class="kpi-grid">
    ${kpi('Active endpoints',st.active_jobs??all.filter(j=>j.enabled).length,`${all.length} registered`,'cyan')}
    ${kpi('Runs · 24h',st.runs_24h??0,`${st.success_24h??0} ok · ${st.retried_24h??0} retried`,'blue')}
    ${kpi('Success rate',rate,deltaTxt,delta!=null&&delta<0?'red':'green')}
    ${kpi('Needs attention',st.needs_attention??0,`${st.open_alerts??0} open alert${(st.open_alerts??0)===1?'':'s'}`,'red')}
  </div>
  <div class="toolbar"><div class="searchbox">${iconSet.search}<input id="job-search" placeholder="Filter by name, service or URL…" value="${esc(state.query||'')}"><kbd>/</kbd></div><span class="live-dot">● Live</span></div>
  <div class="chips-row">${chips.map(([k,label])=>`<button class="chip ${f===k?'active':''}" data-ep-filter="${k}">${label}</button>`).join('')}</div>
  <div class="tier-legend">${Object.entries(TIER_INFO).map(([k,[name]])=>`<span>${tierBadge(k)}${esc(name)}</span>`).join('')}<span class="dim">${TIER_NOTE}</span></div>
  <div class="table-card"><div class="table-title"><div><h2>Endpoint inventory</h2><span class="muted">${filtered.length} of ${all.length} endpoints${f!=='all'||q?' · filtered':''}</span></div></div>
  <div class="table-wrap"><table class="table"><thead><tr><th class="sortable" data-sort="name" tabindex="0" role="button">Endpoint<span class="sort-ind">${state.sortDir==='desc'?'↓':'↑'}</span></th><th>Request</th><th>Schedule</th><th>Next run</th><th><span class="th-help" title="A = read-only · B = writes data · C = high impact. ${TIER_NOTE}">Tier <span aria-hidden="true">?</span></span></th><th>Retry</th><th>Last run</th><th class="col-actions">Actions</th></tr></thead><tbody>
  ${rows.map(j=>`<tr><td data-label="Endpoint"><strong>${esc(j.name)}</strong><small>${esc(j.service)} · ${esc(j.key)}</small></td>
    <td data-label="Request"><span class="method">${esc(j.method)}</span><small class="mono dim trunc" title="${esc(j.url)}">${esc(j.url.replace(/^https?:\/\//,''))}</small></td>
    <td data-label="Schedule"><strong class="mono">${esc(j.cron)}</strong><small>${esc(scheduleWords(j))}</small><small>${esc(j.timezone)}${j.clock_aware?' · clock-aware':''}</small></td>
    <td data-label="Next run">${nextRunCell(j)}</td>
    <td data-label="Tier">${tierBadge(j.risk_tier)}</td>
    <td data-label="Retry">${retryWords(j)}</td>
    <td data-label="Last run">${lastRunCell(j)}</td>
    <td class="cell-actions" data-label="Actions"><button class="row-action" data-play="${j.id}" title="Run now">▶</button><button class="row-action" data-preview="${j.id}" title="Test connection">⚡</button><button class="row-action" data-edit="${j.id}" title="Edit">✎</button><button class="row-action danger" data-del-job="${j.id}" title="Delete">✕</button></td></tr>`).join('')||`<tr><td colspan="8" class="empty">No endpoints match this filter.</td></tr>`}
  </tbody></table></div>
  ${filtered.length>PAGE_SIZE?`<div class="pager"><span class="muted">Showing ${rows.length} of ${filtered.length} endpoints</span><div class="pages">${Array.from({length:pages},(_,i)=>`<button class="chip ${page===i+1?'active':''}" data-ep-page="${i+1}">${i+1}</button>`).join('')}</div></div>`:`<div class="pager"><span class="muted">Showing ${rows.length} of ${filtered.length} endpoints</span></div>`}
  </div>`;
  const si=document.getElementById('job-search');
  if(si)si.oninput=e=>{state.query=e.target.value;state.epPage=1;keepFocus(()=>endpointsView(c))};
  document.querySelectorAll('[data-ep-filter]').forEach(b=>b.onclick=()=>{state.epFilter=b.dataset.epFilter;state.epPage=1;endpointsView(c)});
  document.querySelectorAll('[data-ep-page]').forEach(b=>b.onclick=()=>{state.epPage=Number(b.dataset.epPage);endpointsView(c)});
  bindGotoAllowlist(c);
}


function timelineView(c){
  const tl=state.timeline||{lanes:[],load:[],failures7d:[],range:'today',timezone:srvClock.zone,date:''};
  const lanes=tl.lanes||[];
  const load=tl.load||[];
  const sel=state.tlHour;
  const dayLabel=(()=>{
    const start=new Date(tl.from||Date.now()),end=new Date(tl.to||Date.now());
    if(isNaN(start))return '';
    const f=(d,opt)=>d.toLocaleDateString(undefined,opt);
    if((tl.days||1)===1)return f(start,{weekday:'long',day:'2-digit',month:'long',year:'numeric'});
    const last=new Date(end.getTime()-86400000);
    return `${f(start,{day:'2-digit',month:'short'})} – ${f(last,{day:'2-digit',month:'short',year:'numeric'})}`;
  })();
  const sticky=load.filter(l=>l.jobs>=3).sort((a,b)=>b.jobs-a.jobs).slice(0,2);
  const cellOf=(cell,h)=>{
    const runs=cell?.runs||0,fail=cell?.fail||0,missed=cell?.missed||0;
    if(!runs&&!missed)return `<span class="lane-cell c-empty${sel!=null&&sel!==h?' c-dim':''}" title="${String(h).padStart(2,'0')}:00 — no run"></span>`;
    const kind=missed&&!fail?'missed':(fail?'bad':'ok');
    const why=missed&&!fail?'window missed':(fail?`${fail} failed of ${runs}`:'ran normally');
    return `<span class="lane-cell c-${kind}${sel===h?' c-picked':(sel!=null?' c-dim':'')}" title="${String(h).padStart(2,'0')}:00 — ${why}"></span>`;
  };
  const hourRuns=sel==null?null:lanes.map(l=>({lane:l,cell:l.cells[sel]})).filter(x=>(x.cell?.runs||0)+(x.cell?.missed||0)>0);

  c.innerHTML=`
  <div class="tl-head">
    <div class="tl-head-main">
      <h2>${tl.range==='week'?'Last 7 days':'Today'}</h2>
      <p class="muted">${esc(dayLabel)} · ${esc(tl.timezone||'')} · ${lanes.length} endpoint${lanes.length===1?'':'s'} with activity</p>
    </div>
    <div class="seg" role="tablist" aria-label="Range">
      <button type="button" class="seg-btn ${tl.range==='today'?'active':''}" data-tl-range="today">Today</button>
      <button type="button" class="seg-btn ${tl.range==='week'?'active':''}" data-tl-range="week">Last 7 days</button>
    </div>
  </div>
  ${sticky.length?`<div class="banner warn">⚠ ${sticky.map(s=>`<strong>${s.jobs} endpoints</strong> are scheduled at ${String(s.hour).padStart(2,'0')}:00`).join(' · ')} — spread them out so one hour does not carry everything.</div>`:''}
  <div class="table-card tl-card">
    <div class="tl-scroll-wrap">
    <div class="tl-scroll">
    <div class="tl-hours">${Array.from({length:24},(_,h)=>`<button type="button" class="hr ${sel===h?'active':''}" data-tl-hour="${h}" title="Inspect ${String(h).padStart(2,'0')}:00">${String(h).padStart(2,'0')}</button>`).join('')}</div>
    ${lanes.length?lanes.map(l=>`<div class="lane" data-tl-lane="${esc(l.job_id)}" tabindex="0" role="button" title="Open run history for ${esc(l.name)}">
      <div class="lane-name"><strong>${esc(l.name)}</strong><small class="mono">${esc(l.cron||'')}</small></div>
      <div class="lane-strip">${Array.from({length:24},(_,h)=>cellOf(l.cells[h],h)).join('')}</div>
      <div class="lane-sum ${l.fail?'bad':(l.missed?'warn':'ok')}">${l.fail?`${l.fail} failed`:(l.missed?`${l.missed} missed`:(l.runs?`${l.runs} ok`:'quiet'))}</div>
    </div>`).join(''):`<div class="empty"><strong>Nothing ran in this window</strong><p>Either the scheduler is off or no endpoint is due in this range. Turn the scheduler on in Settings, or press play on an endpoint.</p></div>`}
    </div>
    </div>
    <div class="tl-legend">
      <span><i class="lg lg-none"></i>no run</span>
      <span><i class="lg ok"></i>ran normally</span>
      <span><i class="lg bad"></i>failed</span>
      <span><i class="lg missed"></i>window missed</span>
      <span class="tl-hint">Click a lane for its run history · click an hour to inspect it</span>
    </div>
  </div>
  ${sel!=null?`<div class="table-card tl-hour-panel">
    <div class="table-title"><div><h2>${String(sel).padStart(2,'0')}:00 — ${esc(tl.timezone)}</h2><span class="muted">${hourRuns.length} endpoint${hourRuns.length===1?'':'s'} active in this hour</span></div><button type="button" class="ghost" id="tl-hour-clear">Clear</button></div>
    ${hourRuns.length?`<div class="list-rows">${hourRuns.map(x=>`<div class="list-row"><span class="run-dot ${x.cell.fail?'bad':(x.cell.missed?'warn':'ok')}"></span><strong>${esc(x.lane.name)}</strong><span class="dim">${x.cell.runs} run${x.cell.runs===1?'':'s'}${x.cell.fail?` · ${x.cell.fail} failed`:''}${x.cell.missed?` · ${x.cell.missed} missed`:''}</span></div>`).join('')}</div>`:`<div class="empty">No run in this hour.</div>`}
  </div>`:''}
  <div class="table-card">
    <div class="table-title"><div><h2>Least reliable · last 7 days</h2><span class="muted">Failed calls and windows the scheduler never fired</span></div></div>
    ${(tl.failures7d||[]).length?`<div class="list-rows">${tl.failures7d.map(f=>`<div class="list-row"><span class="pill ${f.failed?'bad':'warn'}"><i></i>${f.failed?'failed':'missed'}</span><strong>${esc(f.name)}</strong><span class="dim">${f.failed?`${f.failed} failed`:''}${f.failed&&f.missed?' · ':''}${f.missed?`${f.missed} missed`:''}</span></div>`).join('')}</div>`:`<div class="empty">No failures in the last 7 days.</div>`}
  </div>`;

  document.querySelectorAll('[data-tl-range]').forEach(b=>b.onclick=async()=>{
    state.tlRange=b.dataset.tlRange;state.tlHour=null;
    await loadPage();
  });
  document.querySelectorAll('[data-tl-hour]').forEach(b=>b.onclick=()=>{
    const h=Number(b.dataset.tlHour);state.tlHour=state.tlHour===h?null:h;timelineView(c);
  });
  const clr=$('#tl-hour-clear');if(clr)clr.onclick=()=>{state.tlHour=null;timelineView(c)};
  document.querySelectorAll('[data-tl-lane]').forEach(row=>{
    const open=()=>openLaneHistory(row.dataset.tlLane);
    row.onclick=open;
    row.onkeydown=e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();open()}};
  });
}

async function openLaneHistory(jobID){
  const job=(state.jobs||[]).find(j=>j.id===jobID);
  let runs=[];
  try{runs=(await api('/runs?job_id='+encodeURIComponent(jobID)+'&limit=12')).data||[]}catch(e){notify(e.message);return}
  const rowsHtml=runs.map(r=>`<tr class="clickable" data-open-run="${esc(r.id)}">
    <td>${badge(statusLabel(r.status))}</td>
    <td>${stamp(r.created_at)}<br><small class="dim">${relTime(r.created_at)}</small></td>
    <td class="mono">${r.http_status==null?'—':esc(r.http_status)}</td>
    <td class="mono">${r.duration_ms==null?'—':esc(r.duration_ms)+' ms'}</td>
    <td>${r.trigger==='schedule'?'schedule':(r.trigger||'manual')}</td></tr>`).join('');
  const x=modalBase(`History · ${esc(job?.name||'endpoint')}`, `
    <div class="detail-grid">
      <div><span class="k">Endpoint</span><span class="v">${esc(job?.name||'')} <span class="mono dim">${esc(job?.key||'')}</span></span></div>
      <div><span class="k">Schedule</span><span class="v mono">${esc(job?.cron||'')} · ${esc(job?.timezone||'')}</span></div>
      <div><span class="k">Last run</span><span class="v">${job?.last_run_status?`${esc(statusLabel(job.last_run_status))} · ${relTime(job.last_run_at)}`:'never'}</span></div>
    </div>
    ${runs.length?`<div class="table-wrap"><table class="table compact"><thead><tr><th>Status</th><th>When</th><th>HTTP</th><th>Duration</th><th>Trigger</th></tr></thead><tbody>${rowsHtml}</tbody></table></div>`:`<div class="empty">No runs recorded for this endpoint yet.</div>`}
    <div class="actions"><button type="button" class="ghost" id="lane-close">Close</button><button type="button" class="ghost" id="lane-play">▶ Run now</button></div>`);
  x.querySelector('.modal').classList.add('is-wide');
  x.querySelector('#lane-close').onclick=()=>document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());
  x.querySelector('#lane-play').onclick=()=>{document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());play(jobID)};
  x.querySelectorAll('[data-open-run]').forEach(tr=>tr.onclick=()=>{document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());openRunDetail(tr.dataset.openRun)});
}

function runsView(c){
  const all=state.runs||[],q=String(state.runQuery||'').toLowerCase(),fs=state.runStatus||'',ft=state.runTrigger||'';
  const runs=all.filter(r=>(!fs||r.status===fs)&&(!ft||r.trigger===ft)&&(!q||[r.job_name,r.job_key,r.job_url,r.id,r.trigger,r.error,r.triggered_by].some(v=>String(v||'').toLowerCase().includes(q))));
  const ok=runs.filter(r=>r.status==='success').length;
  const fail=runs.filter(r=>['failed','failed_after_retries','error','timeout'].includes(String(r.status))).length;
  const durs=runs.map(r=>r.duration_ms).filter(v=>typeof v==='number').sort((a,b)=>a-b);
  const p95=durs.length?durs[Math.min(durs.length-1,Math.floor(durs.length*0.95))]:null;
  const rate=runs.length?Math.round(ok/runs.length*100)+'%':'—';
  const statusOpts=[['','All statuses'],['success','Succeeded'],['failed','Failed'],['failed_after_retries','Failed after retries'],['timeout','Timed out'],['queued','Queued'],['running','Running']];
  const trigOpts=[['','All triggers'],['manual','Manual'],['schedule','Schedule'],['retry','Retry'],['replay','Replay']];
  c.innerHTML=`<div class="kpi-grid">${kpi('Runs shown',runs.length,all.length+' loaded','cyan')}${kpi('Success rate',rate,runs.length?'Within the current filter':'No runs match','green')}${kpi('Failures',fail,fail?'Needs attention':'None in this view','red')}${kpi('p95 duration',p95==null?'—':p95+' ms',durs.length?durs.length+' timed runs':'Awaiting data','blue')}</div>
  <div class="toolbar"><div class="searchbox">${iconSet.search}<input id="run-search" placeholder="Filter by endpoint, key, run id or error…" value="${esc(state.runQuery||'')}"><kbd>/</kbd></div>
    <div class="filters"><select id="run-status">${statusOpts.map(([v,l])=>`<option value="${v}"${fs===v?' selected':''}>${l}</option>`).join('')}</select>
    <select id="run-trigger">${trigOpts.map(([v,l])=>`<option value="${v}"${ft===v?' selected':''}>${l}</option>`).join('')}</select></div>
    <span class="live-dot">● Live</span></div>
  <div class="table-card"><div class="table-title"><div><h2>Execution history</h2><span class="muted">${runs.length} of ${all.length} runs · newest first</span></div></div>
  <div class="table-wrap"><table class="table runs-table"><thead><tr><th>Endpoint</th><th>Status</th><th>HTTP</th><th>Duration</th><th>Attempts</th><th>Trigger</th><th>Started</th><th class="col-actions">Inspect</th></tr></thead><tbody>
  ${runs.map(r=>{const id=r.id,label=r.job_name||r.job_key||('job '+String(r.job_id||'').slice(0,8));return `<tr class="run-row" data-run-row="${id}" tabindex="0" role="button" aria-label="Inspect run ${esc(id.slice(0,8))}">
    <td data-label="Endpoint"><strong>${esc(label)}</strong><small class="ellipsis">${r.job_method?esc(r.job_method)+' · ':''}${esc(r.job_url?r.job_url.replace(/^https?:\/\//,'').split('/')[0]:(r.job_key||''))}</small><small class="mono dim">#${esc(id.slice(0,8))}</small></td>
    <td data-label="Status"><div class="status-cell">${runStatus(r)}${r.dry_run?'<span class="tag">dry run</span>':''}${r.simulated?'<span class="tag">simulated</span>':''}</div>${r.http_status==null&&r.status!=='success'?runReason(r.error):''}</td>
    <td data-label="HTTP" class="mono num">${r.http_status==null?(r.error&&r.status!=='success'?'<span class="dim" title="No HTTP response — see the reason under Status">none</span>':'—'):esc(r.http_status)}</td>
    <td data-label="Duration" class="mono num">${r.duration_ms==null?'—':esc(r.duration_ms)+' ms'}</td>
    <td data-label="Attempts" class="mono num">${esc(r.attempts)}</td>
    <td data-label="Trigger">${esc(r.trigger)}${r.triggered_by?'<small class="ellipsis">by '+esc(String(r.triggered_by).slice(0,14))+'</small>':''}</td>
    <td data-label="Started"><strong>${relTime(r.created_at)}</strong><small>${stamp(r.created_at)}</small></td>
    <td class="cell-actions" data-label="Inspect"><button class="row-action" data-run-detail="${id}" title="Inspect run" aria-label="Inspect run ${esc(id.slice(0,8))}">→</button></td></tr>`}).join('')||`<tr><td colspan="8" class="empty">No runs match this filter. Clear the search or run an endpoint with ▶.</td></tr>`}
  </tbody></table></div></div>
  ${upcomingSection()}`;
  const rs=document.getElementById('run-search');
  if(rs)rs.oninput=e=>{state.runQuery=e.target.value;keepFocus(()=>runsView(c))};
  const ss=document.getElementById('run-status');
  if(ss)ss.onchange=e=>{state.runStatus=e.target.value;runsView(c)};
  const ts=document.getElementById('run-trigger');
  if(ts)ts.onchange=e=>{state.runTrigger=e.target.value;runsView(c)};
  document.querySelectorAll('[data-run-row]').forEach(tr=>{
    const open=()=>openRunDetail(tr.dataset.runRow);
    tr.onclick=e=>{if(!e.target.closest('[data-run-detail]'))open()};
    tr.onkeydown=e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();open()}};
  });
  document.querySelectorAll('[data-run-detail]').forEach(b=>b.onclick=e=>{e.stopPropagation();openRunDetail(b.dataset.runDetail)});
  bindGotoAllowlist(c);
}

async function openRunDetail(id){
  let r;
  try{r=await api('/runs/'+id)}catch(e){notify(e.message);return}
  const label=r.job_name||r.job_key||('job '+String(r.job_id||'').slice(0,8));
  const core=[['Endpoint',esc(label)+(r.job_key?` <span class="mono dim">${esc(r.job_key)}</span>`:'')],
    ['Status',runStatus(r)+(r.dry_run?' <span class="pill warn"><i></i>dry run</span>':'')+(r.simulated?' <span class="pill warn"><i></i>simulated</span>':'')],
    ['HTTP status',r.http_status==null?'—':'<span class="mono">'+esc(r.http_status)+'</span>'],
    ['Duration',r.duration_ms==null?'—':'<span class="mono">'+esc(r.duration_ms)+' ms</span>'],
    ['Attempts','<span class="mono">'+esc(r.attempts)+'</span>'],
    ['Trigger',esc(r.trigger)+(r.triggered_by?' by '+esc(r.triggered_by):'')]];
  const identity=[['Destination',`<span class="mono">${esc(r.job_method||'')} ${esc(r.job_url||'')}</span>`],
    ['Run id',`<span class="mono">${esc(r.id)}</span> <button type="button" class="mini" id="copy-run-inline" title="Copy run id">copy</button>`]];
  const timing=[['Scheduled for',stamp(r.scheduled_for)],['Started',stamp(r.started_at)],['Finished',stamp(r.finished_at)],['Created',stamp(r.created_at)]];
  if(r.simulated_time)timing.push(['Simulated time',stamp(r.simulated_time)]);
  if(r.assertion_passed!=null)core.push(['Assertions',r.assertion_passed?'<span class="pill good"><i></i>passed</span>':'<span class="pill bad"><i></i>failed</span>']);
  const grid=rows=>`<div class="detail-grid">${rows.map(([k,v])=>`<div><span class="k">${k}</span><span class="v">${v}</span></div>`).join('')}</div>`;
  const attempts=(r.attempts_detail||[]).map(a=>`<li class="tl-item"><div class="tl-head"><span class="pill ${statusClass(a.status)}"><i></i>${esc(statusLabel(a.status))}</span><span class="mono dim">#${esc(a.attempt_no)}</span><span class="mono dim">${a.http_status==null?'—':'HTTP '+esc(a.http_status)}</span><span class="mono dim">${a.duration_ms==null?'—':esc(a.duration_ms)+' ms'}</span></div><div class="tl-body"><span class="muted">${stamp(a.started_at)} → ${stamp(a.finished_at)}</span>${a.error?`<p class="errline">${esc(a.error)}</p>`:''}${a.response_excerpt&&a.error?`<details class="tl-more"><summary>attempt body</summary><pre class="excerpt">${esc(a.response_excerpt)}</pre></details>`:''}${a.next_attempt_at?`<span class="muted">retry at ${stamp(a.next_attempt_at)}</span>`:''}</div></li>`).join('')||'<li class="muted">No attempts recorded yet.</li>';
  const assertions=(r.assertion_results||[]).map(a=>`<tr><td class="mono">${esc(a.kind)}</td><td class="mono">${esc(a.target||'')}</td><td class="mono">${esc(a.expected||'')}</td><td class="mono">${esc(a.actual||'')}</td><td>${a.passed?'<span class="pill good"><i></i>pass</span>':'<span class="pill bad"><i></i>fail</span>'}</td></tr>`).join('');
  let recent='',notifyRow='',job=null;
  try{job=await api('/jobs/'+r.job_id)}catch{}
  try{
    const rr=await api('/runs?job_id='+encodeURIComponent(r.job_id)+'&limit=6');
    const list=(rr.data||[]).filter(z=>z.id!==r.id).slice(0,5);
    if(list.length)recent=`<h3 class="sec">Recent runs for this endpoint</h3><div class="table-wrap"><table class="table compact"><thead><tr><th>Status</th><th>When</th><th>HTTP</th><th>Duration</th></tr></thead><tbody>${list.map(z=>`<tr data-run="${z.id}" class="clickable"><td>${badge(statusLabel(z.status))}</td><td>${stamp(z.created_at)}<br><small class="dim">${relTime(z.created_at)}</small></td><td class="mono">${z.http_status==null?'—':esc(z.http_status)}</td><td class="mono">${z.duration_ms==null?'—':esc(z.duration_ms)+' ms'}</td></tr>`).join('')}</tbody></table></div>`;
  }catch{}
  try{
    const ob=await api('/outbox');
    const mine=(ob.data||[]).filter(o=>o.run_id===r.id);
    if(mine.length)notifyRow=`<h3 class="sec">Notifications</h3><div class="detail-grid">${mine.map(o=>`<div><span class="k">${esc(o.kind||'channel')} · ${esc(o.channel_name||o.channel_id||'')}</span><span class="v">${o.sent_at?`<span class="pill good"><i></i>sent</span> ${stamp(o.sent_at)}`:`<span class="pill warn"><i></i>${esc(o.state||'pending')}</span>${o.attempts?` · ${esc(o.attempts)} attempt(s)`:''}${o.last_error?`<br><small class="dim">${esc(o.last_error)}</small>`:''}`}</span></div>`).join('')}</div>`;
  }catch{}
  const effUrl=(()=>{const x=(r.request_excerpt||'').replace(/^(GET|POST|PUT|PATCH|DELETE|HEAD)\s+/i,'').trim();return x||r.job_url||job?.url||''})();
  const curl=buildCurl(job,r,effUrl);
  const x=modalBase(`Run · ${esc(label)}`, `${grid(core)}${grid(identity)}
    <h3 class="sec">Timing</h3>${grid(timing)}
    ${r.error?`<div class="callout bad"><strong>${r.http_status==null?'Blocked before sending':'Failure'}</strong><p class="mono">${esc(r.error)}</p>${/not on allowlist|tidak ada di allowlist/i.test(r.error)?`<p class="callout-help">The host of this endpoint is not in the egress allowlist, so CronSoul refused the call without contacting it.${state.me?.role==='superadmin'?' <a href="#" data-goto-allowlist>Open the allowlist</a> and add the host.':' Ask a superadmin to add the host.'}</p>`:''}</div>`:''}
    <h3 class="sec">Attempt timeline</h3><ol class="timeline">${attempts}</ol>
    <h3 class="sec">Request</h3>
    <pre class="excerpt" id="req-line">${esc(effUrl?`${r.job_method||job?.method||''} ${effUrl}`:'—')}</pre>
    ${(r.dry_run||r.simulated)?`<div class="detail-grid">
      ${r.dry_run?`<div><span class="k">Dry run</span><span class="v"><span class="pill warn"><i></i>marker sent${job?.dry_run_param?` · <span class="mono">${esc(job.dry_run_param)}=true</span>`:''}</span></span></div>`:''}
      ${r.simulated?`<div><span class="k">Simulated time</span><span class="v mono">${esc(r.simulated_time||'—')}${job?.simulated_time_header?` via ${esc(job.simulated_time_header)}`:''}</span></div>`:''}
    </div>`:''}
    ${job&&Object.keys(job.headers||{}).length?`<p class="k-mini">Configured headers</p><pre class="excerpt">${esc(Object.entries(job.headers).map(([k,v])=>k+': '+v).join('\n'))}</pre>`:''}
    ${job&&job.query&&Object.keys(job.query).length?`<p class="k-mini">Configured query</p><pre class="excerpt">${esc(Object.entries(job.query).map(([k,v])=>k+'='+v).join('\n'))}</pre>`:''}
    ${job&&job.body?`<p class="k-mini">Configured body</p><pre class="excerpt">${esc(JSON.stringify(job.body,null,2))}</pre>`:''}
    ${job?.auth_type&&job.auth_type!=='none'?`<p class="k-mini">Auth</p><pre class="excerpt">${esc(job.auth_type)} · credentials stored encrypted, never shown</pre>`:''}
    <p class="k-mini">cURL</p><pre class="excerpt" id="curl-pre">${esc(curl||'—')}</pre>
    ${r.response_excerpt?`<h3 class="sec">Response${r.response_truncated?' (truncated)':''}</h3><pre class="excerpt">${esc(r.response_excerpt)}</pre>`:''}
    ${notifyRow}
    ${recent}
    ${assertions?`<h3 class="sec">Assertions</h3><div class="table-wrap"><table class="table compact"><thead><tr><th>Kind</th><th>Target</th><th>Expected</th><th>Actual</th><th>Result</th></tr></thead><tbody>${assertions}</tbody></table></div>`:''}
    <div class="actions"><button type="button" class="ghost" id="re-run" ${r.job_id?'':'disabled'}>▶ Run again</button><button type="button" class="ghost" id="copy-curl" ${curl?'':'disabled'}>Copy cURL</button><button type="button" class="ghost" id="close-run">Close</button></div>`);
  x.querySelector('.modal').classList.add('is-wide');
  bindGotoAllowlist(x);
  const copy=()=>{navigator.clipboard?.writeText(r.id);notify('Run id copied')};
  const ci=x.querySelector('#copy-run-inline');if(ci)ci.onclick=copy;
  x.querySelector('#close-run').onclick=()=>document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());
  x.querySelectorAll('[data-run]').forEach(row=>row.onclick=()=>{document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());openRunDetail(row.dataset.run)});
  const cc=x.querySelector('#copy-curl');if(cc&&curl)cc.onclick=async()=>{
    const ok=await copyText(curl);
    if(ok){notify('cURL copied');return}
    // Clipboard APIs need a secure context; on plain http fall back to selecting
    // the block so Ctrl+C works immediately.
    const pre=x.querySelector('#curl-pre');
    if(pre){const range=document.createRange();range.selectNodeContents(pre);const sel=window.getSelection();sel.removeAllRanges();sel.addRange(range);notify('Selected — press Ctrl+C to copy')}
    else notify('Copy is blocked in this browser')
  };
  const rr2=x.querySelector('#re-run');if(rr2&&r.job_id)rr2.onclick=async()=>{rr2.disabled=true;try{const q=await api('/jobs/'+r.job_id+'/run',{method:'POST',body:'{}'});notify('Run queued · '+String(q.run_id||'').slice(0,8));document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove())}catch(e){notify(e.message);rr2.disabled=false}};
}

function play(id){
  const j=state.jobs.find(x=>x.id===id)||{};
  const x=modalBase(`Play · ${esc(j.name||'endpoint')}`, `<div class="detail-grid">
      <div><span class="k">Destination</span><span class="v mono">${esc(j.method||'')} ${esc(j.url||'')}</span></div>
      <div><span class="k">Schedule</span><span class="v mono">${esc(j.cron||'')} · ${esc(j.timezone||'')}</span></div>
      <div><span class="k">Tier</span><span class="v">${tierBadge(j.risk_tier)} ${esc((TIER_INFO[String(j.risk_tier||'A').toUpperCase()]||[''])[0])}${j.clock_aware?' <span class="pill neutral"><i></i>clock-aware</span>':' <span class="pill neutral"><i></i>wall clock</span>'}</span></div>
    </div>
    ${['POST','PUT','PATCH','DELETE'].includes(String(j.method||'').toUpperCase())?`<div class="callout warn"><strong>This run may change data</strong><p>${esc(j.method)} ${esc(j.url||'')} is a write. Use dry run when the endpoint supports it.</p></div>`:''}
    <form id="mform" novalidate>
      <div class="form-grid-2">
        <label>Simulated time<input name="simulated_time" placeholder="2026-01-02T03:04:05Z"><span class="hint">RFC3339. Honoured only when this endpoint is clock-aware.</span></label>
        <label>Mode<select name="mode"><option value="live">Live run</option><option value="dry">Dry run</option></select><span class="hint">A dry run is recorded but sends no changes.</span></label>
      </div>
      <div class="actions"><button type="button" class="ghost" id="play-cancel">Cancel</button><button class="primary" type="submit">▶ Run now</button></div>
    </form>`);
  const form=x.querySelector('form'),submit=form.querySelector('button[type=submit]'),cancel=x.querySelector('#play-cancel');
  submit.addEventListener('click',e=>{e.preventDefault();form.requestSubmit()});
  cancel.onclick=()=>document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());
  form.onsubmit=async e=>{
    e.preventDefault();modalError('');
    const data=Object.fromEntries(new FormData(form));
    const body={};const sim=String(data.simulated_time||'').trim();
    if(sim){if(isNaN(Date.parse(sim))){modalError('Simulated time must be an RFC3339 timestamp, e.g. 2026-01-02T03:04:05Z.');return}body.simulated_time=sim}
    if(data.mode==='dry')body.dry_run=true;
    modalBusy(submit,true);
    try{
      const r=await api('/jobs/'+id+'/run',{method:'POST',body:JSON.stringify(body)});
      document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());
      notify('Run queued · '+String(r.run_id||'').slice(0,8));
      state.page='runs';state.runQuery='';state.runStatus='';state.runTrigger='';
      await loadPage();render();
      setTimeout(()=>openRunDetail(r.run_id),350);
    }catch(err){modalError(err.message)}finally{modalBusy(submit,false,'▶ Run now')}
  };
}

function modalBase(title,body){document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());const x=document.createElement('div');x.className='modal-backdrop';x.innerHTML=`<div class="modal" role="dialog" aria-modal="true"><button class="modal-close" aria-label="Close">×</button><div class="eyebrow">SECURE ACTION</div><h2>${title}</h2>${body}<div class="modal-error" id="modal-error" role="alert" hidden></div></div>`;document.body.append(x);const close=()=>{if(x.dataset.closing)return;x.dataset.closing='1';x.classList.add('is-closing');const done=()=>{if(x.parentNode)x.remove()};x.addEventListener('animationend',done,{once:true});setTimeout(done,240)};requestAnimationFrame(()=>x.classList.add('is-open'));x.querySelector('.modal-close').onclick=close;x.onclick=e=>e.target===x&&close();x.addEventListener('keydown',e=>e.key==='Escape'&&close());x.querySelector('input,select,textarea')?.focus();return x}
// One document-level Escape rule: close the topmost modal first, otherwise the drawer.
document.addEventListener('keydown',e=>{
  if(e.key!=='Escape')return;
  const open=[...document.querySelectorAll('.modal-backdrop')].filter(m=>!m.dataset.closing);
  if(open.length){
    const top=open[open.length-1];
    const btn=top.querySelector('.modal-close');
    if(btn)btn.click();
    e.stopPropagation();
  }
},true);

function modalError(msg){const el=document.querySelector('.modal-error');if(!el)return;if(!msg){el.hidden=true;el.textContent='';return}el.hidden=false;el.textContent=msg}
function modalBusy(btn,busy,label){if(!btn)return;btn.disabled=busy;btn.textContent=busy?'Working…':label}
function modalUser(){const x=modalBase('Add user',`<form id="mform" novalidate><label>Username<input name="username"></label><label>Temporary password<input name="password" type="password"><span class="hint">At least 10 characters; the user must change it at first login.</span></label><label>Role<select name="role"><option>viewer</option><option>qa</option><option>admin</option></select></label><button class="primary wide" type="submit">Create user</button></form>`);const form=x.querySelector('form'),submit=form.querySelector('button');submit.addEventListener('click',e=>{e.preventDefault();form.requestSubmit()});form.onsubmit=async e=>{e.preventDefault();modalError('');const data=Object.fromEntries(new FormData(form));if(!String(data.username||'').trim()){modalError('Enter a username.');return}if(String(data.password||'').length<10){modalError('The temporary password needs at least 10 characters.');return}modalBusy(submit,true);try{await api('/users',{method:'POST',body:JSON.stringify(data)});document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());notify('User created');redesignedRender()}catch(err){modalError(err.message)}finally{modalBusy(submit,false,'Create user')}}}
function modalAllow(){const x=modalBase('Add allowlist pattern',`<form id="mform" novalidate><label>Host, host:port, IP or full URL<input name="pattern" placeholder="api.example.com or https://api.example.com/path"><span class="hint">Only the host is stored. Scheme and path are stripped. IPv6 goes in brackets, e.g. [::1].</span></label><label>Note<input name="note" placeholder="Why is this destination allowed?"></label><button class="primary wide" type="submit">Add pattern</button></form>`);const form=x.querySelector('form'),submit=form.querySelector('button');submit.addEventListener('click',e=>{e.preventDefault();form.requestSubmit()});form.onsubmit=async e=>{e.preventDefault();modalError('');const data=Object.fromEntries(new FormData(form));data.pattern=String(data.pattern||'').trim();if(!data.pattern){modalError('Enter a host, host:port, IP or a full URL.');form.querySelector('input[name=pattern]').focus();return}modalBusy(submit,true);try{await api('/allowlist',{method:'POST',body:JSON.stringify(data)});document.querySelectorAll('.modal-backdrop').forEach(m=>m.remove());notify('Pattern added');redesignedRender()}catch(err){modalError(err.message)}finally{modalBusy(submit,false,'Add pattern')}}}
async function userActionRedesign(b){if(b.dataset.userAction==='reset-password'){const x=modalBase('Reset password',`<form id="mform"><label>New temporary password<input name="password" type="password" minlength="10" required></label><button class="primary wide">Reset password</button></form>`);x.querySelector('form').onsubmit=async e=>{e.preventDefault();try{await api('/users/'+b.dataset.id+'/reset-password',{method:'POST',body:JSON.stringify(Object.fromEntries(new FormData(e.target)))});x.remove();notify('Password reset');redesignedRender()}catch(err){notify(err.message)}};return}if(!confirm(`Confirm ${b.dataset.userAction} for this user?`))return;try{await api('/users/'+b.dataset.id+'/'+b.dataset.userAction,{method:'POST',body:'{}'});notify('User updated');redesignedRender()}catch(e){notify(e.message)}}
async function deleteAllowRedesign(b){if(!confirm('Delete this allowlist entry?'))return;try{await api('/allowlist/'+b.dataset.allowDelete,{method:'DELETE'});notify('Pattern removed');redesignedRender()}catch(e){notify(e.message)}}
draw=redesignedDraw;
