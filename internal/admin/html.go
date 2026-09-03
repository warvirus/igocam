// 카메라 관리 페이지 HTML (Tailwind CSS + 자체 포함)
package admin

const adminHTML = `<!DOCTYPE html>
<html lang="ko">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>igocam Admin</title>
<script src="https://cdn.tailwindcss.com"></script>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Chakra+Petch:wght@400;500;600;700&family=Share+Tech+Mono&display=swap" rel="stylesheet">
<style>
  * { box-sizing: border-box; }
  body { margin:0; min-height:100vh; background:#070b14; font-family:'Share Tech Mono',ui-monospace,monospace; }
  .font-hud { font-family:'Share Tech Mono',ui-monospace,monospace; }
  .font-disp { font-family:'Chakra Petch',ui-sans-serif,sans-serif; }

  /* scanning line overlay */
  .scanlines::after {
    content:''; position:fixed; inset:0; pointer-events:none;
    background: repeating-linear-gradient(0deg, transparent,transparent 2px, rgba(34,211,238,.03) 2px, rgba(34,211,238,.03) 4px);
    z-index:9999;
  }

  /* REC flash */
  @keyframes recblink { 0%,100%{opacity:1} 50%{opacity:.2} }
  .rec-on { animation:recblink 1.2s step-end infinite; }

  /* corner brackets */
  .corners { position:absolute; inset:0; pointer-events:none; }
  .corners::before, .corners::after { content:''; position:absolute; }
  .ct { top:0;left:0;width:28px;height:28px;border-top:2px solid rgba(34,211,238,.6);border-left:2px solid rgba(34,211,238,.6); }
  .cr { top:0;right:0;width:28px;height:28px;border-top:2px solid rgba(34,211,238,.6);border-right:2px solid rgba(34,211,238,.6); }
  .cb { bottom:0;left:0;width:28px;height:28px;border-bottom:2px solid rgba(34,211,238,.6);border-left:2px solid rgba(34,211,238,.6); }
  .cbr { bottom:0;right:0;width:28px;height:28px;border-bottom:2px solid rgba(34,211,238,.6);border-right:2px solid rgba(34,211,238,.6); }

  .login-input { background:rgba(15,23,42,.8); border:1px solid rgba(71,85,105,.5); color:#e2e8f0; }
  .login-input:focus { border-color:#22d3ee; outline:none; box-shadow:0 0 10px rgba(34,211,238,.2); }

  .card:hover { transform:translateY(-2px); box-shadow:0 10px 25px rgba(0,0,0,.4); }
  .snap { width:100%; height:150px; object-fit:cover; background:#1e293b; }
  .badge { display:inline-flex; align-items:center; padding:2px 10px; border-radius:999px; font-size:11px; font-weight:500; }
  .toast { transition:opacity .3s; }
  button:disabled { opacity:0.4; cursor:not-allowed; }
</style>
</head>
<body class="text-slate-200 font-hud scanlines">

<!-- ─── Login View ─── -->
<div id="loginView" class="fixed inset-0 z-50 flex items-center justify-center bg-[#070b14]" style="display:none">
  <div class="relative w-full max-w-sm mx-5">
    <div class="corners"><i class="ct"></i><i class="cr"></i><i class="cb"></i><i class="cbr"></i></div>
    <div class="bg-slate-900/70 backdrop-blur border border-slate-700/60 rounded-sm p-8 shadow-2xl">

      <!-- header -->
      <div class="flex items-center gap-3 mb-6 text-xs tracking-widest">
        <span class="rec-on text-rose-400 font-bold text-sm">&#9679; REC</span>
        <span class="text-cyan-400">LIVE</span>
        <span class="text-slate-500">igocam</span>
      </div>

      <!-- title -->
      <h1 class="font-disp text-3xl font-bold text-white tracking-wider mb-1">igocam</h1>
      <p class="text-cyan-400/80 text-xs tracking-[.25em] mb-8 font-hud">CAMERA CONTROL CENTER</p>

      <!-- error -->
      <div id="loginError" class="text-rose-400 text-xs mb-4 hidden"></div>

      <!-- form -->
      <form id="loginForm" onsubmit="event.preventDefault();doLogin();" class="space-y-4">
        <div>
          <label class="text-xs text-slate-400 tracking-wider block mb-1">USERNAME</label>
          <input id="loginUser" type="text" class="login-input w-full px-4 py-2.5 text-sm rounded" autocomplete="username" required>
        </div>
        <div>
          <label class="text-xs text-slate-400 tracking-wider block mb-1">PASSWORD</label>
          <input id="loginPass" type="password" class="login-input w-full px-4 py-2.5 text-sm rounded" autocomplete="current-password" required>
        </div>
        <button type="submit" class="w-full bg-cyan-600 hover:bg-cyan-500 text-white font-semibold py-2.5 rounded tracking-wider transition text-sm">
          &#9654; 로그인
        </button>
      </form>

      <p class="text-slate-600 text-[10px] text-center mt-6 tracking-widest">igocam v1.3 &middot; 2026</p>
    </div>
  </div>
</div>

<!-- ─── App View ─── -->
<div id="appView" style="display:none">
<div class="max-w-7xl mx-auto p-6">
  <!-- Header -->
  <div class="flex flex-wrap items-center justify-between gap-4 mb-6">
    <div class="flex items-center gap-3">
      <span class="w-3 h-3 rounded-full bg-emerald-500"></span>
      <h1 class="font-disp text-xl font-bold text-white tracking-wider">igocam</h1>
      <span class="text-xs text-slate-500 tracking-widest">CAMERA CONTROL CENTER</span>
    </div>
    <div class="flex flex-wrap gap-2 items-center">
      <button onclick="reloadConfig()" class="bg-cyan-600 hover:bg-cyan-500 text-white px-3 py-1.5 rounded text-xs font-medium">&#x21bb; Reload</button>
      <button id="btnStartAll" onclick="startAll()" class="bg-emerald-600 hover:bg-emerald-500 text-white px-3 py-1.5 rounded text-xs font-medium disabled:opacity-30">&#9654; 전체 시작</button>
      <button id="btnStopAll" onclick="stopAll()" class="bg-rose-600 hover:bg-rose-500 text-white px-3 py-1.5 rounded text-xs font-medium disabled:opacity-30">&#9632; 전체 정지</button>
      <button onclick="pauseStreams()" class="bg-amber-600 hover:bg-amber-500 text-white px-3 py-1.5 rounded text-xs font-medium">&#9208; 멈춤</button>
      <button onclick="resumeStreams()" class="bg-teal-600 hover:bg-teal-500 text-white px-3 py-1.5 rounded text-xs font-medium">&#9654; 재개</button>
      <button onclick="openAddModal()" class="bg-indigo-600 hover:bg-indigo-500 text-white px-3 py-1.5 rounded text-xs font-medium">+ 카메라 추가</button>
      <button onclick="logout()" class="bg-slate-700 hover:bg-slate-600 text-white px-3 py-1.5 rounded text-xs font-medium">로그아웃</button>
    </div>
  </div>

  <!-- Camera Grid -->
  <div id="cameraGrid" class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4"></div>
  <div id="emptyState" class="hidden text-center py-20 text-slate-500">
    <p class="text-4xl mb-4">&#x1f4f7;</p>
    <p class="text-lg font-hud">등록된 카메라가 없습니다.</p>
    <button onclick="openAddModal()" class="mt-4 bg-indigo-600 hover:bg-indigo-500 text-white px-4 py-2 rounded-lg text-sm">첫 카메라 추가</button>
  </div>
</div>
</div>

<!-- Toast -->
<div id="toast" class="toast fixed bottom-4 right-4 hidden bg-slate-800 border border-slate-600 text-white px-4 py-3 rounded-lg shadow-lg z-50" style="display:none"></div>

<!-- Modals (same as before) -->
<div id="modal" class="fixed inset-0 bg-black/60 hidden items-center justify-center z-50 p-4">
  <div class="bg-slate-800 rounded-xl max-w-2xl w-full max-h-[90vh] overflow-y-auto">
    <div class="p-5 border-b border-slate-700 flex justify-between items-center">
      <h2 id="modalTitle" class="text-lg font-bold text-white">카메라 추가</h2>
      <button onclick="closeModal()" class="text-slate-400 hover:text-white text-2xl">&times;</button>
    </div>
    <div class="p-5 grid grid-cols-2 gap-4">
      <div><label class="block text-sm text-slate-400 mb-1">이름</label><input id="f_name" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">소스 (파일경로/URL)</label><input id="f_source" value="./videos/default.mp4" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">HW 가속</label>
        <select id="f_hw_accel" class="w-full bg-slate-700 rounded px-3 py-2 text-sm">
          <option value="videotoolbox">videotoolbox</option>
          <option value="auto">auto</option>
          <option value="cpu">cpu</option>
          <option value="nvenc">nvenc</option>
          <option value="qsv">qsv</option>
        </select></div>
      <div><label class="block text-sm text-slate-400 mb-1">Bypass</label>
        <select id="f_bypass" class="w-full bg-slate-700 rounded px-3 py-2 text-sm">
          <option value="false">false</option><option value="true" selected>true</option>
        </select></div>
      <div><label class="block text-sm text-slate-400 mb-1">ONVIF 포트</label><input id="f_onvif" type="number" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">RTSP 포트</label><input id="f_rtsp" type="number" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">RTMP 포트</label><input id="f_rtmp" type="number" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">go2rtc API 포트</label><input id="f_api" type="number" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">Web 포트</label><input id="f_web" type="number" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">WebRTC 포트</label><input id="f_webrtc" type="number" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">Main 해상도</label>
        <div class="flex gap-2"><input id="f_main_w" type="number" placeholder="1280" class="w-1/2 bg-slate-700 rounded px-3 py-2 text-sm"><input id="f_main_h" type="number" placeholder="720" class="w-1/2 bg-slate-700 rounded px-3 py-2 text-sm"></div></div>
      <div><label class="block text-sm text-slate-400 mb-1">Main FPS</label><input id="f_main_fps" type="number" value="30" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">Main 비트레이트</label><input id="f_main_br" value="1M" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">Sub 해상도</label>
        <div class="flex gap-2"><input id="f_sub_w" type="number" placeholder="320" class="w-1/2 bg-slate-700 rounded px-3 py-2 text-sm"><input id="f_sub_h" type="number" placeholder="180" class="w-1/2 bg-slate-700 rounded px-3 py-2 text-sm"></div></div>
      <div><label class="block text-sm text-slate-400 mb-1">Sub FPS</label><input id="f_sub_fps" type="number" value="30" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">Sub 비트레이트</label><input id="f_sub_br" value="500K" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
    </div>
    <div class="p-5 border-t border-slate-700 flex justify-end gap-2">
      <button onclick="closeModal()" class="bg-slate-600 hover:bg-slate-500 text-white px-4 py-2 rounded-lg">취소</button>
      <button id="modalSubmit" onclick="submitCamera()" class="bg-indigo-600 hover:bg-indigo-500 text-white px-4 py-2 rounded-lg">저장</button>
    </div>
  </div>
</div>

<div id="delModal" class="fixed inset-0 bg-black/60 hidden items-center justify-center z-50 p-4">
  <div class="bg-slate-800 rounded-xl max-w-md w-full p-5">
    <h2 class="text-lg font-bold text-white mb-3">카메라 삭제</h2>
    <p class="text-slate-300 mb-5">정말로 '<span id="delName"></span>' 카메라를 삭제하시겠습니까?</p>
    <div class="flex justify-end gap-2">
      <button onclick="closeDelModal()" class="bg-slate-600 hover:bg-slate-500 text-white px-4 py-2 rounded-lg">취소</button>
      <button onclick="confirmDelete()" class="bg-rose-600 hover:bg-rose-500 text-white px-4 py-2 rounded-lg">삭제</button>
    </div>
  </div>
</div>

<script>
let cameras = [];
let editingId = null;
let deleteId = null;
let auth = null;

/* ─── Auth ─── */
function loadAuth(){
  try{ auth = JSON.parse(localStorage.getItem('igocam_auth')) }catch(e){ auth=null }
  if(auth && auth.user && auth.pass){
    document.getElementById('loginView').style.display='none';
    document.getElementById('appView').style.display='block';
    refresh();
  } else {
    document.getElementById('loginView').style.display='flex';
    document.getElementById('appView').style.display='none';
  }
}

function getAuthHeader(){
  return auth && auth.user ? 'Basic '+btoa(auth.user+':'+auth.pass) : '';
}

async function doLogin(){
  var u = document.getElementById('loginUser').value;
  var p = document.getElementById('loginPass').value;
  document.getElementById('loginError').classList.add('hidden');
  try {
    var r = await fetch('/api/auth/login', {
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({username:u,password:p})
    });
    if(!r.ok) throw new Error();
    auth = {user:u, pass:p};
    localStorage.setItem('igocam_auth', JSON.stringify(auth));
    document.getElementById('loginView').style.display='none';
    document.getElementById('appView').style.display='block';
    refresh();
  } catch(e){
    auth=null;
    var errEl = document.getElementById('loginError');
    errEl.textContent = '인증에 실패했습니다. 다시 시도하세요.';
    errEl.classList.remove('hidden');
  }
}

function logout(){
  localStorage.removeItem('igocam_auth');
  auth=null;
  document.getElementById('loginView').style.display='flex';
  document.getElementById('appView').style.display='none';
}

/* ─── API ─── */
async function api(url, method, body){
  var opt = { method: method||'GET', headers:{} };
  var h = getAuthHeader();
  if(h) opt.headers['Authorization'] = h;
  if(body){ opt.headers['Content-Type']='application/json'; opt.body=JSON.stringify(body); }
  var r = await fetch(url, opt);
  var data = await r.json().catch(function(){ return {}; });
  if(!r.ok) throw new Error(data.error || ('HTTP '+r.status));
  return data;
}

function toast(msg, ok){
  var t = document.getElementById('toast');
  t.textContent = (ok===false?'\u274c ':'\u2705 ')+msg;
  t.style.display='block';
  setTimeout(function(){ t.style.display='none'; }, 3000);
}

async function refresh(){
  try {
    cameras = (await api('/api/cameras')) || [];
    render();
  } catch(e){ toast(e.message, false); }
}

function statusBadge(s){
  var map = { running: ['bg-emerald-500/20 text-emerald-400','\u25cf running'],
              paused: ['bg-amber-500/20 text-amber-400','\u23f8 paused'],
              stopped: ['bg-rose-500/20 text-rose-400','\u25cf stopped'] };
  var m = map[s] || map.stopped;
  return '<span class="badge '+m[0]+'">'+m[1]+'</span>';
}

function render(){
  var grid = document.getElementById('cameraGrid');
  var empty = document.getElementById('emptyState');
  if (!cameras || !grid) return;
  empty.classList.toggle('hidden', cameras.length>0);
  grid.innerHTML = cameras.map(function(c){
    return '<div class="card bg-slate-800 rounded-xl overflow-hidden transition-all cursor-pointer" onclick="window.open(\''+c.web_ui_url+'\',\'_blank\')">' +
      '<img class="snap" src="'+c.snapshot_url+'" alt="'+c.name+'" onerror="this.style.opacity=0">' +
      '<div class="p-4">' +
        '<div class="flex items-center justify-between mb-2">' +
          '<h3 class="font-semibold text-white truncate">'+c.name+'</h3>' +
          statusBadge(c.status) +
        '</div>' +
        '<p class="text-xs text-slate-400 truncate mb-2">'+c.source+'</p>' +
        '<div class="flex items-center gap-2 text-xs text-slate-400 mb-3">' +
          '<span class="bg-slate-700 px-2 py-0.5 rounded">'+c.hw_accel+'</span>' +
          '<span class="bg-slate-700 px-2 py-0.5 rounded">:'+c.onvif_port+'</span>' +
        '</div>' +
        '<div class="flex gap-2">' +
          '<button onclick="event.stopPropagation();openEditModal(\''+c.id+'\')" class="flex-1 bg-slate-700 hover:bg-slate-600 text-white text-xs px-3 py-2 rounded-lg">수정</button>' +
          '<button onclick="event.stopPropagation();openDelModal(\''+c.id+'\',\''+c.name+'\')" class="flex-1 bg-rose-600/80 hover:bg-rose-500 text-white text-xs px-3 py-2 rounded-lg">삭제</button>' +
        '</div>' +
      '</div>' +
    '</div>';
  }).join('');
  var sr = (cameras||[]).some(function(c){ return c.status!=='stopped'; });
  var bs = document.getElementById('btnStartAll');
  var bp = document.getElementById('btnStopAll');
  if(bs) bs.disabled = sr;
  if(bp) bp.disabled = !sr;
}

/* Service controls */
async function reloadConfig(){ try{ var d=await api('/api/reload','POST'); toast('Reload: +'+d.added.length+' / ~'+d.updated.length+' / -'+d.removed.length); await refresh(); }catch(e){toast(e.message,false);} }
async function startAll(){ try{ await api('/api/start-all','POST'); toast('전체 시작'); await refresh(); }catch(e){toast(e.message,false);} }
async function stopAll(){ try{ await api('/api/stop-all','POST'); toast('전체 정지'); await refresh(); }catch(e){toast(e.message,false);} }
async function pauseStreams(){ try{ await api('/api/pause-streams','POST'); toast('스트림 멈춤'); await refresh(); }catch(e){toast(e.message,false);} }
async function resumeStreams(){ try{ await api('/api/resume-streams','POST'); toast('스트림 재개'); await refresh(); }catch(e){toast(e.message,false);} }

/* Modals */
function openAddModal(){
  editingId=null;
  document.getElementById('modalTitle').textContent='카메라 추가';
  ['f_name','f_main_w','f_main_h','f_main_fps','f_main_br','f_sub_w','f_sub_h','f_sub_fps','f_sub_br'].forEach(function(id){ document.getElementById(id).value=''; });
  document.getElementById('f_source').value='./videos/default.mp4';
  document.getElementById('f_hw_accel').value='videotoolbox';
  document.getElementById('f_bypass').value='true';
  // 해상도 기본값
  document.getElementById('f_main_w').value=1280; document.getElementById('f_main_h').value=720;
  document.getElementById('f_sub_w').value=320; document.getElementById('f_sub_h').value=180;
  document.getElementById('f_main_fps').value=30; document.getElementById('f_main_br').value='1M';
  document.getElementById('f_sub_fps').value=30; document.getElementById('f_sub_br').value='500K';
  // 포트 자동 제안: 기존 카메라 최대 포트 +10
  api('/api/ports/available').then(function(p){
    document.getElementById('f_onvif').value=p.onvif_port;
    document.getElementById('f_rtsp').value=p.rtsp_port;
    document.getElementById('f_rtmp').value=p.rtmp_port;
    document.getElementById('f_api').value=p.go2rtc_api_port;
    document.getElementById('f_web').value=p.web_port;
    document.getElementById('f_webrtc').value=p.webrtc_port;
  }).catch(function(){});
  document.getElementById('modal').classList.remove('hidden'); document.getElementById('modal').classList.add('flex');
}
function openEditModal(id){
  var c = cameras.find(function(x){return x.id===id;}); if(!c) return;
  editingId=id;
  document.getElementById('modalTitle').textContent='카메라 수정';
  api('/api/cameras/'+id).then(function(cfg){
    document.getElementById('f_name').value=cfg.name;
    document.getElementById('f_source').value=cfg.source||'';
    document.getElementById('f_hw_accel').value=cfg.hw_accel||'videotoolbox';
    document.getElementById('f_bypass').value=String(cfg.bypass||false);
    document.getElementById('f_onvif').value=cfg.onvif_port; document.getElementById('f_rtsp').value=cfg.rtsp_port;
    document.getElementById('f_rtmp').value=cfg.rtmp_port; document.getElementById('f_api').value=cfg.go2rtc_api_port;
    document.getElementById('f_web').value=cfg.web_port; document.getElementById('f_webrtc').value=cfg.webrtc_port;
    document.getElementById('f_main_w').value=cfg.main_width; document.getElementById('f_main_h').value=cfg.main_height;
    document.getElementById('f_main_fps').value=cfg.main_fps; document.getElementById('f_main_br').value=cfg.main_bitrate;
    document.getElementById('f_sub_w').value=cfg.sub_width; document.getElementById('f_sub_h').value=cfg.sub_height;
    document.getElementById('f_sub_fps').value=cfg.sub_fps; document.getElementById('f_sub_br').value=cfg.sub_bitrate;
    document.getElementById('modal').classList.remove('hidden'); document.getElementById('modal').classList.add('flex');
  }).catch(function(e){ toast(e.message,false); });
}
function closeModal(){ document.getElementById('modal').classList.add('hidden'); document.getElementById('modal').classList.remove('flex'); }
async function submitCamera(){
  var body = {
    name: document.getElementById('f_name').value,
    source: document.getElementById('f_source').value,
    hw_accel: document.getElementById('f_hw_accel').value,
    bypass: document.getElementById('f_bypass').value==='true',
    onvif_port: +document.getElementById('f_onvif').value,
    rtsp_port: +document.getElementById('f_rtsp').value,
    rtmp_port: +document.getElementById('f_rtmp').value,
    go2rtc_api_port: +document.getElementById('f_api').value,
    web_port: +document.getElementById('f_web').value,
    webrtc_port: +document.getElementById('f_webrtc').value,
    main_width: +document.getElementById('f_main_w').value,
    main_height: +document.getElementById('f_main_h').value,
    main_fps: +document.getElementById('f_main_fps').value,
    main_bitrate: document.getElementById('f_main_br').value,
    sub_width: +document.getElementById('f_sub_w').value,
    sub_height: +document.getElementById('f_sub_h').value,
    sub_fps: +document.getElementById('f_sub_fps').value,
    sub_bitrate: document.getElementById('f_sub_br').value,
  };
  try {
    if(editingId) { await api('/api/cameras/'+editingId,'PUT',body); toast('카메라 수정 완료'); }
    else { await api('/api/cameras','POST',body); toast('카메라 추가 완료'); }
    closeModal(); await refresh();
  } catch(e){ toast(e.message,false); }
}
function openDelModal(id,name){ deleteId=id; document.getElementById('delName').textContent=name; document.getElementById('delModal').classList.remove('hidden'); document.getElementById('delModal').classList.add('flex'); }
function closeDelModal(){ document.getElementById('delModal').classList.add('hidden'); document.getElementById('delModal').classList.remove('flex'); }
async function confirmDelete(){ try{ await api('/api/cameras/'+deleteId,'DELETE'); toast('삭제 완료'); closeDelModal(); await refresh(); }catch(e){toast(e.message,false);} }

/* Init */
loadAuth();
setInterval(refresh, 5000);
</script>
</body>
</html>`
