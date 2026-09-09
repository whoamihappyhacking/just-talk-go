'use strict';
const demo = document.querySelector('#demo');
const text = document.querySelector('#demo-text');
const status = document.querySelector('#demo-status');
const record = document.querySelector('#record-button');
const retry = document.querySelector('#retry-button');
const key = document.querySelector('#capsule-key');
let state = 'idle';
let timer;
const transcript = '把录音缓存下来。如果识别失败，就保留这段音频，等我按 R 的时候再重试。';
function show(next) {
  clearTimeout(timer);
  state = next;
  demo.classList.toggle('recording', next === 'recording' || next === 'processing');
  demo.classList.toggle('failed', next === 'failed');
  retry.hidden = next !== 'failed';
  record.disabled = next === 'processing' || next === 'failed';
  const views = {
    idle: ['按一下，开始表达', 'REC', '点击下方麦克风，体验一次语音输入。', '开始录音演示'],
    recording: ['正在录音 · 再按一下结束', 'STOP', '把录音缓存下来。如果识别失败……', '结束录音演示'],
    processing: ['正在识别缓存的录音…', '···', '正在处理刚才那段录音，请稍等。', '正在处理演示'],
    failed: ['已缓存 · 按 R 才重试', 'R', '连接暂时失败。录音已保留，不会自动重试。点击「R · 重试」继续这次演示。', '请按 R 重试缓存录音'],
    done: ['文字已就位 · 演示完成', '✓', transcript, '再次开始录音演示']
  };
  const view = views[next];
  status.textContent = view[0]; key.textContent = view[1]; text.textContent = view[2]; record.setAttribute('aria-label', view[3]);
  if (next === 'processing') timer = setTimeout(() => show('done'), 1300);
}
record.addEventListener('click', () => {
  if (state === 'recording') show('processing');
  else if (state === 'idle' || state === 'done') show('recording');
});
retry.addEventListener('click', () => { if (state === 'failed') show('processing'); });
document.querySelector('#failure-button').addEventListener('click', () => show('failed'));
document.querySelector('#reset-button').addEventListener('click', () => show('idle'));
demo.addEventListener('keydown', event => {
  if (event.repeat || event.ctrlKey || event.metaKey || event.altKey) return;
  if (event.key.toLowerCase() === 'r' && state === 'failed') { event.preventDefault(); show('processing'); }
  if (event.key === 'Escape') { event.preventDefault(); show('idle'); }
});
const snippets = {
  linux: { command: '# 检查环境与配置\n./just-talk --doctor\n\n# 启动配置界面\n./just-talk', tip: 'Wayland 需 input 读取权限，并安装 wl-clipboard 和 wtype。' },
  macos: { command: '# 检查环境与配置\n./just-talk --doctor\n\n# 启动配置界面\n./just-talk', tip: '请为启动 Just Talk 的终端应用授予辅助功能与麦克风权限。' },
  windows: { command: '# PowerShell：检查环境与配置\n.\\just-talk.exe --doctor\n\n# 启动配置界面\n.\\just-talk.exe', tip: '支持 Windows 10 / 11，无需额外安装 ffmpeg、SoX 或 C 编译器。' }
};
const tabs = [...document.querySelectorAll('[data-platform]')];
const copy = document.querySelector('#copy-command');
let copyTimer;
function selectPlatform(tab) {
  tabs.forEach(item => { const selected = item === tab; item.setAttribute('aria-selected', String(selected)); item.tabIndex = selected ? 0 : -1; });
  const snippet = snippets[tab.dataset.platform];
  document.querySelector('#commands').textContent = snippet.command;
  document.querySelector('#platform-tip').textContent = snippet.tip;
  document.querySelector('#command-panel').setAttribute('aria-labelledby', tab.id);
  copy.textContent = '复制命令';
}
tabs.forEach((tab, index) => {
  tab.addEventListener('click', () => selectPlatform(tab));
  tab.addEventListener('keydown', event => {
    let next;
    if (event.key === 'ArrowRight') next = (index + 1) % tabs.length;
    if (event.key === 'ArrowLeft') next = (index + tabs.length - 1) % tabs.length;
    if (event.key === 'Home') next = 0;
    if (event.key === 'End') next = tabs.length - 1;
    if (next !== undefined) { event.preventDefault(); selectPlatform(tabs[next]); tabs[next].focus(); }
  });
});
copy.addEventListener('click', async () => {
  const command = document.querySelector('#commands').textContent;
  let copied = false;
  try { if (navigator.clipboard && window.isSecureContext) { await navigator.clipboard.writeText(command); copied = true; } } catch (_) { /* Fall back for plain HTTP. */ }
  if (!copied) {
    const field = document.createElement('textarea'); field.value = command; field.style.position = 'fixed'; field.style.opacity = '0'; document.body.append(field); field.select();
    try { copied = document.execCommand('copy'); } catch (_) { copied = false; }
    field.remove(); copy.focus();
  }
  copy.textContent = copied ? '已复制 ✓' : '请选中命令复制';
  clearTimeout(copyTimer); copyTimer = setTimeout(() => { copy.textContent = '复制命令'; }, 2500);
});
