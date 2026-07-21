(function(){
'use strict';
var TOKEN_KEY='phira_mp_admin_token';
var MAX_POINTS=150, MAX_LOG_DOM=800;
var $=function(id){return document.getElementById(id);};
var token=localStorage.getItem(TOKEN_KEY)||'';
// GUI 窗口模式：服务器把本机专用 token 放在 URL 片段里（不经过网络请求与日志），读取后立即清除
var hashToken=/[#&]token=([^&]+)/.exec(location.hash||'');
if(hashToken){
  token=decodeURIComponent(hashToken[1]);
  localStorage.setItem(TOKEN_KEY,token);
  try{history.replaceState(null,'',location.pathname);}catch(e){}
}
var ws=null, wsRetry=null, pollTimer=null, upTimer=null;
var samples=[];            // {timestamp,cpuPercent,rss,heapUsed,heapTotal}
var rooms=[];              // admin rooms data
var players=[];            // 全部在线用户（含未进房间的大厅玩家）
var pollTick=0;
var openRooms={};          // roomid -> true（保持展开状态）
var cpuCores=1, sysTotal=0, uptimeBase=0, uptimeAt=0;
var cmdHistory=[], histIdx=-1;
var unseen=0;             // 滚动离开底部时累计的未读日志数（用于「跳到最新」气泡）
// 用于 Tab 补全的命令名（与 CLI 命令表保持一致）
var CMDS=['approve','ban','banlist','banroom','broadcast','contest','deny','disband','help',
  'ipblacklist','kick','list','maxusers','pending','reject','replay','roomcreation','roomsay',
  'rooms','say','shutdown','stop','unban','unbanroom','user','users'];

function esc(s){return String(s).replace(/[&<>"']/g,function(c){
  return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];});}

/* ===== 深浅色主题 ===== */
var THEME_KEY='phira_mp_gui_theme';
function applyTheme(t){
  document.documentElement.setAttribute('data-theme',t);
  var btn=$('themeToggle');
  if(btn)btn.textContent=t==='dark'?'☀':'☾';
  drawCharts();
}
var savedTheme=localStorage.getItem(THEME_KEY);
var sysLight=window.matchMedia?window.matchMedia('(prefers-color-scheme: light)'):null;
applyTheme(savedTheme==='light'||savedTheme==='dark'?savedTheme:(sysLight&&sysLight.matches?'light':'dark'));
// 未手动选择时跟随系统深浅色切换
if(sysLight&&sysLight.addEventListener){
  sysLight.addEventListener('change',function(e){
    if(!localStorage.getItem(THEME_KEY))applyTheme(e.matches?'light':'dark');
  });
}
$('themeToggle').addEventListener('click',function(){
  var next=document.documentElement.getAttribute('data-theme')==='dark'?'light':'dark';
  localStorage.setItem(THEME_KEY,next);
  applyTheme(next);
});
function pad(n){return n<10?'0'+n:''+n;}
function fmtTime(ts){var d=new Date(ts);return pad(d.getHours())+':'+pad(d.getMinutes())+':'+pad(d.getSeconds());}
function fmtBytes(n){
  if(n>=1073741824)return (n/1073741824).toFixed(2)+' GB';
  return (n/1048576).toFixed(1)+' MB';}
function fmtUptime(sec){
  sec=Math.floor(sec);
  var d=Math.floor(sec/86400),h=Math.floor(sec%86400/3600),m=Math.floor(sec%3600/60),s=sec%60;
  var hms=pad(h)+':'+pad(m)+':'+pad(s);
  return d>0?d+'天 '+hms:hms;}

function req(path,opts){
  opts=opts||{};
  opts.headers=opts.headers||{};
  opts.headers['x-admin-token']=token;
  if(opts.body)opts.headers['content-type']='application/json';
  return fetch(path,opts).then(function(r){
    if(r.status===401||r.status===403){throw {auth:true,status:r.status};}
    return r.json();
  });}

/* ===== 登录 ===== */
function showLogin(msg){
  $('login').classList.remove('hidden');
  $('app').classList.add('hidden');
  $('loginErr').textContent=msg||'';
  stopAll();
}
function stopAll(){
  if(pollTimer){clearInterval(pollTimer);pollTimer=null;}
  if(upTimer){clearInterval(upTimer);upTimer=null;}
  if(wsRetry){clearTimeout(wsRetry);wsRetry=null;}
  if(ws){try{ws.close();}catch(e){}ws=null;}
}
$('loginForm').addEventListener('submit',function(e){
  e.preventDefault();
  var t=$('tokenInput').value.trim();
  if(!t){$('loginErr').textContent='令牌不能为空';return;}
  token=t;
  req('/admin/metrics?history=1').then(function(data){
    localStorage.setItem(TOKEN_KEY,token);
    enter(data);
  }).catch(function(err){
    $('loginErr').textContent=(err&&err.auth)?'令牌无效或未授权':'连接失败，请检查服务状态';
  });
});
$('logout').addEventListener('click',function(){
  localStorage.removeItem(TOKEN_KEY);token='';
  showLogin('');
});

/* ===== 进入主界面 ===== */
function enter(metrics){
  $('login').classList.add('hidden');
  $('app').classList.remove('hidden');
  applyMetrics(metrics,true);
  pollTimer=setInterval(poll,2000);
  upTimer=setInterval(tickUptime,1000);
  req('/admin/rooms').then(function(d){if(d&&d.ok){rooms=d.rooms||[];renderRooms();}}).catch(function(){});
  fetchPlayers();
  req('/admin/console/logs?limit=300').then(function(d){
    if(d&&d.ok){clearLog();(d.lines||[]).forEach(function(l){appendServerLog(l);});}
  }).catch(function(){});
  connectWs();
}

function poll(){
  req('/admin/metrics').then(function(d){applyMetrics(d,false);}).catch(function(err){
    if(err&&err.auth)showLogin('令牌已失效，请重新登录');
  });
  // 玩家列表低频轮询（每 4 秒），房间变化时由 admin_update 即时触发刷新
  pollTick++;
  if(pollTick%2===0)fetchPlayers();
}

function fetchPlayers(){
  req('/admin/users').then(function(d){
    if(d&&d.ok){players=d.users||[];renderPlayers();}
  }).catch(function(){});
}

// 仅在数值变化时更新并触发一次脉冲动画（避免每 2 秒无谓重排，也让变化更易察觉）
function setCounter(id,val){
  var el=$(id);if(!el)return;
  var s=String(val);
  if(el.textContent===s)return;
  el.textContent=s;
  var c=el.parentNode;
  c.classList.remove('bump');void c.offsetWidth;c.classList.add('bump');
}
function applyMetrics(d,withHistory){
  if(!d||!d.ok)return;
  if(d.server){
    $('srvName').textContent=d.server.name||'Phira MP';
    document.title=(d.server.name||'Phira MP')+' · 服务端控制台';
  }
  $('srvSub').textContent='v'+((d.server&&d.server.version)||'-')+' · PID '+(d.process?d.process.pid:'-')
    +' · '+(d.process?(d.process.runtime||d.process.nodeVersion||'-'):'-');
  if(d.cpu)cpuCores=d.cpu.cores||1;
  if(d.memory&&d.memory.systemTotal)sysTotal=d.memory.systemTotal;
  uptimeBase=d.process?d.process.uptime:0;uptimeAt=Date.now();
  var pprof=$('pprofURL');
  if(pprof&&d.process&&d.process.pprofURL){pprof.href=d.process.pprofURL;pprof.textContent=d.process.pprofURL;pprof.classList.remove('hidden');}
  tickUptime();
  if(d.business){
    setCounter('cUsers',d.business.onlineUsers);
    setCounter('cRooms',d.business.activeRooms);
    setCounter('cSessions',d.business.activeSessions);
    setCounter('cWs',d.business.wsConnections);
  }
  if(withHistory&&d.history&&d.history.length){samples=d.history.slice(-MAX_POINTS);}
  else if(d.memory&&d.cpu){
    samples.push({timestamp:d.timestamp,cpuPercent:d.cpu.percent,
      rss:d.memory.rss,heapUsed:d.memory.heapUsed,heapTotal:d.memory.heapTotal});
    if(samples.length>MAX_POINTS)samples.splice(0,samples.length-MAX_POINTS);
  }
  drawCharts();
}

function tickUptime(){
  if(!uptimeAt)return;
  $('uptime').textContent=fmtUptime(uptimeBase+(Date.now()-uptimeAt)/1000);
}

/* ===== 图表 ===== */
function drawSeries(canvas,arr,maxV,color,fixedMax){
  var dpr=window.devicePixelRatio||1;
  var w=canvas.clientWidth,h=canvas.clientHeight;
  if(!w||!h){canvas._geo=null;return;}
  if(canvas.width!==w*dpr||canvas.height!==h*dpr){canvas.width=w*dpr;canvas.height=h*dpr;}
  var ctx=canvas.getContext('2d');
  ctx.setTransform(dpr,0,0,dpr,0,0);
  ctx.clearRect(0,0,w,h);
  // 网格（颜色随主题）
  ctx.strokeStyle=getComputedStyle(document.documentElement).getPropertyValue('--chart-grid').trim()||'rgba(255,255,255,.05)';
  ctx.lineWidth=1;
  for(var gy=1;gy<4;gy++){var y=h*gy/4;ctx.beginPath();ctx.moveTo(0,y+.5);ctx.lineTo(w,y+.5);ctx.stroke();}
  if(arr.length<2){canvas._geo=null;return;}
  var mx=fixedMax||Math.max(maxV,1);
  var step=w/(MAX_POINTS-1);
  var x0=w-(arr.length-1)*step;
  canvas._geo={x0:x0,step:step,n:arr.length,mx:mx,h:h};
  ctx.beginPath();
  for(var i=0;i<arr.length;i++){
    var px=x0+i*step,py=h-Math.min(arr[i]/mx,1)*(h-6)-3;
    if(i===0)ctx.moveTo(px,py);else ctx.lineTo(px,py);
  }
  ctx.strokeStyle=color;ctx.lineWidth=1.5;
  ctx.shadowColor=color;ctx.shadowBlur=6;
  ctx.stroke();
  ctx.shadowBlur=0;
  ctx.lineTo(x0+(arr.length-1)*step,h);ctx.lineTo(x0,h);ctx.closePath();
  var grad=ctx.createLinearGradient(0,0,0,h);
  grad.addColorStop(0,color.replace(')',',.28)').replace('rgb','rgba'));
  grad.addColorStop(1,color.replace(')',',.02)').replace('rgb','rgba'));
  ctx.fillStyle=grad;ctx.fill();
}
function drawCharts(){
  var rss=samples.map(function(s){return s.rss;});
  var cpu=samples.map(function(s){return s.cpuPercent;});
  var maxRss=Math.max.apply(null,rss.concat([1]))*1.25;
  // 曲线颜色从 CSS 变量读取（RGB 三元组），随主题切换
  var cs=getComputedStyle(document.documentElement);
  var memColor='rgb('+(cs.getPropertyValue('--chart-mem').trim()||'63,220,151')+')';
  var cpuColor='rgb('+(cs.getPropertyValue('--chart-cpu').trim()||'88,196,220')+')';
  drawSeries($('memChart'),rss,maxRss,memColor);
  drawSeries($('cpuChart'),cpu,100,cpuColor,100);
  var last=samples[samples.length-1];
  if(last){
    $('memNow').textContent=fmtBytes(last.rss);
    $('memDetail').textContent='堆 '+fmtBytes(last.heapUsed)+' / '+fmtBytes(last.heapTotal)
      +(sysTotal?' · 系统 '+fmtBytes(sysTotal):'');
    $('cpuNow').textContent=last.cpuPercent.toFixed(1)+'%';
    $('cpuDetail').textContent=cpuCores+' 核';
  }
}
window.addEventListener('resize',drawCharts);

// 悬停读数：在曲线上显示十字准线 + 该时刻的精确数值与时间
function attachChartHover(wrapId,canvasId,crossId,dotId,tipId,pick,fmt,colorVar,colorDef){
  var wrap=$(wrapId),canvas=$(canvasId),cross=$(crossId),dot=$(dotId),tip=$(tipId);
  if(!wrap||!canvas)return;
  function move(e){
    var geo=canvas._geo;
    if(!geo||!samples.length){wrap.classList.remove('hot');return;}
    var rect=canvas.getBoundingClientRect();
    var i=Math.round((e.clientX-rect.left-geo.x0)/geo.step);
    if(i<0)i=0;if(i>geo.n-1)i=geo.n-1;
    var s=samples[samples.length-geo.n+i];
    if(!s){wrap.classList.remove('hot');return;}
    var v=pick(s);
    var sx=geo.x0+i*geo.step;
    var sy=geo.h-Math.min(v/geo.mx,1)*(geo.h-6)-3;
    var col='rgb('+(getComputedStyle(document.documentElement).getPropertyValue(colorVar).trim()||colorDef)+')';
    cross.style.left=sx+'px';
    dot.style.left=sx+'px';dot.style.top=sy+'px';dot.style.background=col;dot.style.borderColor=col;
    tip.style.left=Math.max(36,Math.min(rect.width-36,sx))+'px';
    tip.innerHTML='<b>'+fmt(v)+'</b><span class="ts">'+fmtTime(s.timestamp)+'</span>';
    wrap.classList.add('hot');
  }
  wrap.addEventListener('mousemove',move);
  wrap.addEventListener('mouseleave',function(){wrap.classList.remove('hot');});
}
attachChartHover('memWrap','memChart','memCross','memDot','memTip',
  function(s){return s.rss;},fmtBytes,'--chart-mem','63,220,151');
attachChartHover('cpuWrap','cpuChart','cpuCross','cpuDot','cpuTip',
  function(s){return s.cpuPercent;},function(v){return v.toFixed(1)+'%';},'--chart-cpu','88,196,220');

/* ===== WebSocket 连接 ===== */
function setConn(ok,text){
  $('connDot').classList.toggle('off',!ok);
  $('connText').textContent=text;
}
function connectWs(){
  var url=(location.protocol==='https:'?'wss://':'ws://')+location.host+'/ws';
  try{ws=new WebSocket(url);}catch(e){scheduleReconnect();return;}
  ws.onopen=function(){
    setConn(true,'已连接');
    ws.send(JSON.stringify({type:'console_subscribe',token:token}));
    ws.send(JSON.stringify({type:'admin_subscribe',token:token}));
  };
  ws.onmessage=function(ev){
    var msg;try{msg=JSON.parse(ev.data);}catch(e){return;}
    if(msg.type==='console_subscribed'){
      clearLog();(msg.data.lines||[]).forEach(function(l){appendServerLog(l);});
    }else if(msg.type==='console_log'){
      appendServerLog(msg.data);
    }else if(msg.type==='admin_update'){
      if(msg.data&&msg.data.changes&&msg.data.changes.rooms){
        rooms=msg.data.changes.rooms;renderRooms();fetchPlayers();
      }
    }else if(msg.type==='error'&&msg.message==='unauthorized'){
      showLogin('令牌已失效，请重新登录');
    }
  };
  ws.onclose=function(){setConn(false,'重连中…');scheduleReconnect();};
  ws.onerror=function(){try{ws.close();}catch(e){}};
}
function scheduleReconnect(){
  if(wsRetry||!token||!$('login').classList.contains('hidden'))return;
  wsRetry=setTimeout(function(){wsRetry=null;connectWs();},3000);
}

/* ===== 控制台 ===== */
function logNearBottom(){
  var el=$('log');
  return el.scrollHeight-el.scrollTop-el.clientHeight<48;
}
function trimLog(){
  // 末尾的命令输入行（cmdForm）永远保留，只回收最旧的日志节点
  var el=$('log'),line=$('cmdForm');
  while(el.childNodes.length>MAX_LOG_DOM&&el.firstChild&&el.firstChild!==line)el.removeChild(el.firstChild);
}
function clearLog(){
  $('log').querySelectorAll('.ll').forEach(function(n){if(n.parentNode)n.parentNode.removeChild(n);});
  unseen=0;updateJump();
}
function appendLine(html,cls){
  var el=$('log'),line=$('cmdForm');
  var stick=logNearBottom();
  var div=document.createElement('div');
  div.className='ll'+(cls?' '+cls:'');
  div.innerHTML=html;
  el.insertBefore(div,line);   // 新日志插到输入行上方，输入行始终在最底部
  trimLog();
  if(stick)el.scrollTop=el.scrollHeight;
  else{unseen++;updateJump();}
}
function updateJump(){
  var b=$('jumpBtn');if(!b)return;
  if(unseen>0){b.textContent='↓ '+unseen+' 条新日志';b.classList.add('show');}
  else b.classList.remove('show');
}
function jumpToLatest(){
  var el=$('log');el.scrollTop=el.scrollHeight;unseen=0;updateJump();
}
$('jumpBtn').addEventListener('click',jumpToLatest);
// 用户主动滚回底部时清零未读计数
$('log').addEventListener('scroll',function(){if(logNearBottom()){unseen=0;updateJump();}});
// 点击日志区任意空白处即聚焦命令行（终端体验：哪里都能继续敲）
$('log').addEventListener('click',function(e){
  if(e.target.closest('input,button,a'))return;
  if(window.getSelection&&String(window.getSelection()).length)return;
  $('cmdInput').focus();
});
function appendServerLog(l){
  appendLine('<span class="t">['+fmtTime(l.timestamp)+']</span> <span class="lv lv-'+esc(l.level)+'">['
    +esc(l.level)+']</span> '+esc(l.message));
}
$('cmdForm').addEventListener('submit',function(e){
  e.preventDefault();
  var input=$('cmdInput');
  var cmd=input.value.trim();
  if(!cmd)return;
  input.value='';
  cmdHistory.push(cmd);histIdx=cmdHistory.length;
  appendLine('<span class="t">['+fmtTime(Date.now())+']</span> &gt; '+esc(cmd),'k-cmd');
  req('/admin/console/command',{method:'POST',body:JSON.stringify({command:cmd})})
    .then(function(d){
      if(d&&d.ok){(d.lines||[]).forEach(function(l){appendLine(esc(l.text),'k-'+l.kind);});}
      else appendLine(esc((d&&d.error)||'命令执行失败'),'k-error');
    })
    .catch(function(err){
      if(err&&err.auth)showLogin('令牌已失效，请重新登录');
      else appendLine('命令发送失败：服务不可达','k-error');
    });
});
$('cmdInput').addEventListener('keydown',function(e){
  if(e.key==='ArrowUp'){
    if(histIdx>0){histIdx--;this.value=cmdHistory[histIdx];e.preventDefault();}
  }else if(e.key==='ArrowDown'){
    if(histIdx<cmdHistory.length-1){histIdx++;this.value=cmdHistory[histIdx];}
    else{histIdx=cmdHistory.length;this.value='';}
    e.preventDefault();
  }else if(e.key==='Tab'){
    // 仅补全命令名（第一个词）；多个候选时取最长公共前缀并在控制台列出
    if(this.value.indexOf(' ')!==-1)return;
    var pre=this.value.toLowerCase();
    if(!pre){e.preventDefault();return;}
    var matches=CMDS.filter(function(c){return c.indexOf(pre)===0;});
    if(!matches.length)return;
    e.preventDefault();
    if(matches.length===1){this.value=matches[0]+' ';return;}
    var lcp=matches[0];
    matches.forEach(function(m){while(m.indexOf(lcp)!==0)lcp=lcp.slice(0,-1);});
    this.value=lcp;
    appendLine(esc(matches.join('  ')),'k-out');
  }
});

/* ===== 房间 / 玩家 ===== */
var ST_TEXT={select_chart:'选谱中',waiting_for_ready:'准备中',playing:'游戏中'};
var ST_CLS={select_chart:'st-select',waiting_for_ready:'st-ready',playing:'st-playing'};

// phira.moe 跳转链接：玩家 → /user/:id，谱面 → /chart/:id。ID 不再直接显示，改为 hover 浮动提示。
function lnkUser(name,id,cls,extra){
  cls=cls?cls+' lnk':'lnk';
  return '<a class="'+cls+'" href="https://phira.moe/user/'+(+id)+'" target="_blank" rel="noopener noreferrer" data-tip="ID '+(+id)+'">'+esc(name)+(extra||'')+'</a>';
}
function lnkChart(name,id){
  return '<a class="lnk" href="https://phira.moe/chart/'+(+id)+'" target="_blank" rel="noopener noreferrer" data-tip="ID '+(+id)+'">'+esc(name)+'</a>';
}

// 浮动 ID 提示（桌面端 hover 显示在元素上方；fixed 定位避免被 overflow 容器裁剪）。
(function(){
  var tip=null;
  function show(t){
    var txt=t.getAttribute('data-tip');if(!txt)return;
    if(!tip){tip=document.createElement('div');tip.className='floattip';document.body.appendChild(tip);}
    tip.textContent=txt;
    var r=t.getBoundingClientRect();
    tip.style.left=(r.left+r.width/2)+'px';
    tip.style.top=(r.top-6)+'px';
    tip.classList.add('show');
  }
  function hide(){if(tip)tip.classList.remove('show');}
  document.addEventListener('mouseover',function(e){var t=e.target.closest&&e.target.closest('[data-tip]');if(t)show(t);});
  document.addEventListener('mouseout',function(e){var t=e.target.closest&&e.target.closest('[data-tip]');if(t)hide();});
  document.addEventListener('mousedown',hide); // 点击跳转/开始滚动前先隐藏
})();

function renderRooms(){
  var el=$('roomList');
  if(!rooms.length){el.innerHTML='<div class="empty">暂无房间</div>';return;}
  var html='';
  rooms.forEach(function(r){
    var stRaw=r.state&&r.state.type?r.state.type:'select_chart';
    var st=(Object.prototype.hasOwnProperty.call(ST_CLS,stRaw)&&Object.prototype.hasOwnProperty.call(ST_TEXT,stRaw))?stRaw:'select_chart';
    var curUsers=Number(r.current_users); if(!isFinite(curUsers))curUsers=0;
    var maxUsers=Number(r.max_users); if(!isFinite(maxUsers))maxUsers=0;
    var curMons=Number(r.current_monitors); if(!isFinite(curMons))curMons=0;
    var flags='';
    if(r.locked)flags+='<span class="flag" title="已锁定">[锁]</span>';
    if(r.cycle)flags+='<span class="flag" title="循环模式">[循]</span>';
    if(r.live)flags+='<span class="flag" title="直播开启">[播]</span>';
    var users='';
    (r.users||[]).forEach(function(u){
      var cls='chip'+(u.is_host?' host':'')+(u.connected?'':' offline');
      var extra='';
      if(st==='playing'){
        if(u.aborted)extra=' ×';
        else if(u.finished)extra=' ✓';
      }
      users+=lnkUser(u.name,u.id,cls,extra);
    });
    var mons='';
    (r.monitors||[]).forEach(function(m){
      mons+=lnkUser(m.name,m.id,'chip'+(m.connected?'':' offline'));
    });
    html+='<div class="room'+(openRooms[r.roomid]?' open':'')+'" data-rid="'+esc(r.roomid)+'">'
      +'<div class="room-head" tabindex="0" role="button" aria-expanded="'+(openRooms[r.roomid]?'true':'false')+'">'
      +'<span class="rid">'+esc(r.roomid)+'</span>'
      +'<span class="badge '+ST_CLS[st]+'">'+esc(ST_TEXT[st])+'</span>'+flags
      +'<span class="room-cnt">'+esc(String(curUsers))+'/'+esc(String(maxUsers))
      +(curMons?' +'+esc(String(curMons))+'观':'')+'</span>'
      +'<span class="chev" aria-hidden="true">&rsaquo;</span>'
      +'</div>'
      +'<div class="room-body">'
      +'<div class="row"><span class="k">房主：</span>'+(r.host?lnkUser(r.host.name,r.host.id):'-')+'</div>'
      +(r.chart?'<div class="row"><span class="k">谱面：</span>'+lnkChart(r.chart.name,r.chart.id)+'</div>':'')
      +'<div class="row"><span class="k">玩家：</span>'+(users||'<span class="k">无</span>')+'</div>'
      +(mons?'<div class="row"><span class="k">观战：</span>'+mons+'</div>':'')
      +'</div></div>';
  });
  el.innerHTML=html;
  el.querySelectorAll('.room-head').forEach(function(head){
    function toggle(){
      var room=head.parentNode,rid=room.getAttribute('data-rid');
      var open=!room.classList.contains('open');
      room.classList.toggle('open',open);
      head.setAttribute('aria-expanded',open?'true':'false');
      if(open)openRooms[rid]=true;else delete openRooms[rid];
    }
    head.addEventListener('click',toggle);
    head.addEventListener('keydown',function(e){
      if(e.key==='Enter'||e.key===' '){e.preventDefault();toggle();}
    });
  });
}
function renderPlayers(){
  var el=$('playerList');
  if(!players.length){el.innerHTML='<div class="empty">暂无玩家</div>';return;}
  var html='';
  players.forEach(function(u){
    var role=u.monitor?'观战':'玩家';
    if(!u.connected)role+='·离线';
    html+='<div class="player-row">'+lnkUser(u.name,u.id,'pname')
      +(u.banned?'<span class="pban">已封禁</span>':'')
      +'<span class="prole">'+role+'</span>'
      +'<span class="proom'+(u.room?'':' lobby')+'">'+(u.room?esc(u.room):'大厅')+'</span></div>';
  });
  el.innerHTML=html;
}

/* ===== 标签页 ===== */
$('tabRooms').addEventListener('click',function(){switchTab(true);});
$('tabPlayers').addEventListener('click',function(){switchTab(false);});
function switchTab(showRooms){
  $('tabRooms').classList.toggle('active',showRooms);
  $('tabPlayers').classList.toggle('active',!showRooms);
  $('tabRooms').setAttribute('aria-selected',showRooms?'true':'false');
  $('tabPlayers').setAttribute('aria-selected',showRooms?'false':'true');
  $('roomList').classList.toggle('hidden',!showRooms);
  $('playerList').classList.toggle('hidden',showRooms);
}

/* ===== 启动 ===== */
if(token){
  req('/admin/metrics?history=1').then(enter).catch(function(err){
    showLogin((err&&err.auth)?'令牌已失效，请重新登录':'');
  });
}else{
  showLogin('');
}
})();
