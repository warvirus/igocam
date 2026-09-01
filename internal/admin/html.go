// 카메라 관리 페이지 HTML (Tailwind CSS, 자체 포함)
package admin

const adminHTML = `<!DOCTYPE html>
<html lang="ko">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>igocam Admin</title>
<script src="https://cdn.tailwindcss.com"></script>
<style>
  body { background: #0f172a; }
  .card:hover { transform: translateY(-2px); box-shadow: 0 10px 25px rgba(0,0,0,.4); }
  .snap { width: 100%; height: 150px; object-fit: cover; background: #1e293b; }
  .badge { @apply inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium; }
  .toast { transition: opacity .3s; }
  button:disabled { opacity: 0.4; cursor: not-allowed; }
</style>
</head>
<body class="text-slate-200">
<div class="max-w-7xl mx-auto p-6">
  <!-- Header -->
  <div class="flex flex-col md:flex-row md:items-center justify-between gap-4 mb-6">
    <div>
      <h1 class="text-2xl font-bold text-white flex items-center gap-2">
        <span class="w-3 h-3 rounded-full bg-emerald-500 inline-block"></span>
        igocam Admin
      </h1>
      <p class="text-slate-400 text-sm mt-1">가상 카메라 관리 대시보드</p>
    </div>
    <div class="flex flex-wrap gap-2">
      <button onclick="reloadConfig()" class="bg-cyan-600 hover:bg-cyan-500 text-white px-4 py-2 rounded-lg text-sm font-medium">🔄 Reload</button>
      <button id="btnStartAll" onclick="startAll()" class="bg-emerald-600 hover:bg-emerald-500 text-white px-4 py-2 rounded-lg text-sm font-medium">▶ 전체 시작</button>
      <button id="btnStopAll" onclick="stopAll()" class="bg-rose-600 hover:bg-rose-500 text-white px-4 py-2 rounded-lg text-sm font-medium">■ 전체 정지</button>
      <button onclick="pauseStreams()" class="bg-amber-600 hover:bg-amber-500 text-white px-4 py-2 rounded-lg text-sm font-medium">⏸ 멈춤</button>
      <button onclick="resumeStreams()" class="bg-teal-600 hover:bg-teal-500 text-white px-4 py-2 rounded-lg text-sm font-medium">▶ 재개</button>
      <button onclick="openAddModal()" class="bg-indigo-600 hover:bg-indigo-500 text-white px-4 py-2 rounded-lg text-sm font-medium">+ 카메라 추가</button>
    </div>
  </div>

  <!-- Camera Grid -->
  <div id="cameraGrid" class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4"></div>
  <div id="emptyState" class="hidden text-center py-20 text-slate-500">
    <p class="text-4xl mb-4">📷</p>
    <p class="text-lg">등록된 카메라가 없습니다.</p>
    <button onclick="openAddModal()" class="mt-4 bg-indigo-600 hover:bg-indigo-500 text-white px-4 py-2 rounded-lg">첫 카메라 추가</button>
  </div>
</div>

<!-- Add/Edit Modal -->
<div id="modal" class="fixed inset-0 bg-black/60 hidden items-center justify-center z-50 p-4">
  <div class="bg-slate-800 rounded-xl max-w-2xl w-full max-h-[90vh] overflow-y-auto">
    <div class="p-5 border-b border-slate-700 flex justify-between items-center">
      <h2 id="modalTitle" class="text-lg font-bold text-white">카메라 추가</h2>
      <button onclick="closeModal()" class="text-slate-400 hover:text-white text-2xl">&times;</button>
    </div>
    <div class="p-5 grid grid-cols-2 gap-4">
      <div><label class="block text-sm text-slate-400 mb-1">이름</label><input id="f_name" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
      <div><label class="block text-sm text-slate-400 mb-1">소스 (파일경로/URL)</label><input id="f_source" class="w-full bg-slate-700 rounded px-3 py-2 text-sm"></div>
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
          <option value="false">false</option><option value="true">true</option>
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

<!-- Delete Confirm -->
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

<!-- Toast -->
<div id="toast" class="toast fixed bottom-4 right-4 hidden bg-slate-800 border border-slate-600 text-white px-4 py-3 rounded-lg shadow-lg z-50"></div>

<script>
let cameras = [];
let editingId = null;
let deleteId = null;

async function api(url, method='GET', body=null) {
  const opt = { method, headers: {} };
  if (body) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  const r = await fetch(url, opt);
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || ('HTTP ' + r.status));
  return data;
}
function toast(msg, ok=true) {
  const t = document.getElementById('toast');
  t.textContent = (ok ? '✅ ' : '❌ ') + msg;
  t.classList.remove('hidden');
  setTimeout(() => t.classList.add('hidden'), 3000);
}
async function refresh() {
  try {
    cameras = await api('/api/cameras');
    render();
  } catch(e) { toast(e.message, false); }
}
function statusBadge(s) {
  const map = { running: ['bg-emerald-500/20 text-emerald-400', '● running'],
                paused: ['bg-amber-500/20 text-amber-400', '⏸ paused'],
                stopped: ['bg-rose-500/20 text-rose-400', '● stopped'] };
  const [cls, label] = map[s] || map.stopped;
  return '<span class="badge '+cls+'">'+label+'</span>';
}
function render() {
  const grid = document.getElementById('cameraGrid');
  const empty = document.getElementById('emptyState');
  empty.classList.toggle('hidden', cameras.length > 0);
  grid.innerHTML = cameras.map(c => (
    '<div class="card bg-slate-800 rounded-xl overflow-hidden transition-all cursor-pointer" onclick="window.open(\''+c.web_ui_url+'\',\'_blank\')">' +
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
    '</div>'
  )).join('');
  // 서비스 상태에 따라 전체 시작/정지 버튼 활성화 제어
  var serviceRunning = cameras.some(function(c) { return c.status !== 'stopped'; });
  var btnStart = document.getElementById('btnStartAll');
  var btnStop = document.getElementById('btnStopAll');
  if (btnStart) btnStart.disabled = serviceRunning;
  if (btnStop) btnStop.disabled = !serviceRunning;
}
// Reload
async function reloadConfig() {
  try { const d = await api('/api/reload','POST'); toast('Reload 완료: +'+d.added.length+' / ~'+d.updated.length+' / -'+d.removed.length); await refresh(); }
  catch(e){ toast(e.message,false); }
}
async function startAll(){ try{ await api('/api/start-all','POST'); toast('전체 시작'); await refresh(); }catch(e){toast(e.message,false);} }
async function stopAll(){ try{ await api('/api/stop-all','POST'); toast('전체 정지'); await refresh(); }catch(e){toast(e.message,false);} }
async function pauseStreams(){ try{ await api('/api/pause-streams','POST'); toast('스트림 멈춤'); await refresh(); }catch(e){toast(e.message,false);} }
async function resumeStreams(){ try{ await api('/api/resume-streams','POST'); toast('스트림 재개'); await refresh(); }catch(e){toast(e.message,false);} }

// Modal
function openAddModal() {
  editingId = null;
  document.getElementById('modalTitle').textContent = '카메라 추가';
  ['f_name','f_source','f_onvif','f_rtsp','f_rtmp','f_api','f_web','f_webrtc','f_main_w','f_main_h','f_main_fps','f_main_br','f_sub_w','f_sub_h','f_sub_fps','f_sub_br'].forEach(id=>document.getElementById(id).value='');
  document.getElementById('f_hw_accel').value='videotoolbox';
  document.getElementById('f_bypass').value='false';
  document.getElementById('f_main_fps').value=30; document.getElementById('f_main_br').value='1M';
  document.getElementById('f_sub_fps').value=30; document.getElementById('f_sub_br').value='500K';
  document.getElementById('modal').classList.remove('hidden'); document.getElementById('modal').classList.add('flex');
}
function openEditModal(id) {
  const c = cameras.find(x=>x.id===id); if(!c) return;
  editingId = id;
  document.getElementById('modalTitle').textContent = '카메라 수정';
  fetch('/api/cameras/'+id).then(r=>r.json()).then(cfg=>{
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
  }).catch(e=>toast(e.message,false));
}
function closeModal(){ document.getElementById('modal').classList.add('hidden'); document.getElementById('modal').classList.remove('flex'); }
async function submitCamera() {
  const body = {
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
    main_width: +document.getElementById('f_main_w').value, main_height: +document.getElementById('f_main_h').value,
    main_fps: +document.getElementById('f_main_fps').value, main_bitrate: document.getElementById('f_main_br').value,
    sub_width: +document.getElementById('f_sub_w').value, sub_height: +document.getElementById('f_sub_h').value,
    sub_fps: +document.getElementById('f_sub_fps').value, sub_bitrate: document.getElementById('f_sub_br').value,
  };
  try {
    if (editingId) { await api('/api/cameras/'+editingId,'PUT',body); toast('카메라 수정 완료'); }
    else { await api('/api/cameras','POST',body); toast('카메라 추가 완료'); }
    closeModal(); await refresh();
  } catch(e){ toast(e.message,false); }
}
// Delete
function openDelModal(id,name){ deleteId=id; document.getElementById('delName').textContent=name; document.getElementById('delModal').classList.remove('hidden'); document.getElementById('delModal').classList.add('flex'); }
function closeDelModal(){ document.getElementById('delModal').classList.add('hidden'); document.getElementById('delModal').classList.remove('flex'); }
async function confirmDelete(){
  try { await api('/api/cameras/'+deleteId,'DELETE'); toast('삭제 완료'); closeDelModal(); await refresh(); }
  catch(e){ toast(e.message,false); }
}
// Init
refresh();
setInterval(refresh, 5000);
</script>
</body>
</html>`
