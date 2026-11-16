const home = document.getElementById('home-view');
const modelsView = document.getElementById('models-view');
const consoleView = document.getElementById('console-view');
const homeButton = document.getElementById('view-home');
const consoleButton = document.getElementById('view-console');
const previousVersion = document.getElementById('previous-version');
const search = document.getElementById('model-search');
const modelCards = [...document.querySelectorAll('[data-model]')];
const filterButtons = [...document.querySelectorAll('[data-filter]')];
const dialog = document.getElementById('preview-dialog');
let activeFilter = 'all';

function showView(name, updateHistory = true) {
  home.hidden = name !== 'home';
  consoleView.hidden = name !== 'console';
  modelsView.hidden = name !== 'models';
  homeButton.setAttribute('aria-pressed', String(name === 'home'));
  consoleButton.setAttribute('aria-pressed', String(name === 'console'));
  previousVersion.href = '../v1.1/index.html#' + name;
  for (const menu of document.querySelectorAll('details[open]')) menu.open = false;
  if (updateHistory && location.hash !== '#' + name) history.pushState(null, '', '#' + name);
  window.scrollTo({top:0, behavior:'instant'});
}

function filterModels() {
  const query = search.value.trim().toLowerCase();
  let count = 0;
  for (const card of modelCards) {
    const categoryMatches = activeFilter === 'all' || card.dataset.categories.split(' ').includes(activeFilter);
    const searchMatches = card.dataset.model.toLowerCase().includes(query);
    card.hidden = !categoryMatches || !searchMatches;
    if (!card.hidden) count += 1;
  }
  document.getElementById('model-empty').hidden = count !== 0;
  document.getElementById('model-count').textContent = '展示 ' + count + ' 个系列';
  for (const button of filterButtons) button.setAttribute('aria-pressed', String(button.dataset.filter === activeFilter));
}

const previews = {
  contact: {title:'联系银月', description:'接入遇到问题，或有使用建议，都可以从这里找到我们。', items:['正式页面将在这里提供售后联系方式与直接联系入口。','反馈问题时附上调用时间和错误信息，请勿发送完整密钥。'], note:'联系方式尚待确认，本预览未填写虚构客服或群号。'},
  community: {title:'在同一片月光下', description:'交流使用经验，关注银月的新消息。', items:['正式页面将在这里提供交流群入口与可复制群号。'], note:'交流群尚待确认，本预览不跳转到第三方群组。'},
  notice: {title:'初次见面，欢迎来到银月。', description:'愿你的每一个想法，都有回响。', items:['从「模型与价格」了解模型，再进入控制台准备密钥。','接入时，按所用工具的要求填写接口地址和模型名称。'], note:'这是一封公告排版示例；实际服务消息将在正式页面发布。'},
  start: {title:'从银月开始接入', description:'选好模型后，准备账户、可用额度和 API 密钥。', items:['查看模型支持的接口协议与价格。','进入控制台创建密钥，并填写客户端配置。','发起一次调用，在使用记录中核对结果。'], note:'当前为设计预览，不会注册账户或产生费用。'},
  gpt: {title:'GPT 系列', description:'模型详情与计价说明的展示示例。', items:['查看具体模型版本、能力与接口协议。','分别核对输入与输出计价及适用条件。','确认你的客户端所需端点是否受支持。'], note:'本稿未接入实时供应与价格，系列名称不代表当前可用承诺。'},
  claude: {title:'Claude 系列', description:'模型详情与计价说明的展示示例。', items:['查看具体模型版本与上下文限制。','核对输入、输出及适用的缓存计费规则。','确认客户端使用的协议和参数支持范围。'], note:'本稿未接入实时供应与价格，系列名称不代表当前可用承诺。'},
  gemini: {title:'Gemini 系列', description:'模型详情与计价说明的展示示例。', items:['查看具体模型版本与支持的输入类型。','核对不同能力对应的计费单位。','确认所需协议与端点，再配置调用。'], note:'本稿未接入实时供应与价格，系列名称不代表当前可用承诺。'},
  billing: {title:'把每次调用的费用看清楚', description:'不同模型与使用方式可能有不同计价规则。', items:['留意价格单位，区分输入、输出及适用的缓存计费。','确认价格适用的模型、分组与条件。','调用后按使用记录核对用量、费用和结果。'], note:'本预览不提供实时报价，也不会改变任何计费规则。'},
  guide: {title:'接入前，准备这三项', description:'先确认工具支持自定义接口，且与所选模型的协议匹配。', items:['接口地址：使用平台接入说明给出的地址。','API 密钥：从控制台创建，勿在公开内容中泄露。','模型名称：填写可用模型列表中的准确名称。'], note:'不同客户端的字段和端点要求可能不同，请以对应接入指引为准。'},
  support: {title:'调用遇到问题时', description:'先记录错误信息与调用时间，再逐项排查。', items:['检查模型名称、接口地址与协议。','核对额度、密钥权限以及调用限制。','查看公告；联系支持时请勿提供完整 API 密钥。'], note:'此处为帮助入口示例，不代表实时服务状态或售后时效承诺。'},
  keys: {title:'API 密钥', description:'创建、复制与管理调用密钥的入口。', items:['为不同工具分别使用密钥，便于管理。','根据实际需求配置权限与限额。','在使用记录中查看调用结果。'], note:'当前是界面设计预览，不会创建或展示真实密钥。'},
  wallet: {title:'钱包与充值', description:'账户余额与充值入口的展示示例。', items:['查看账户可用额度。','充值前核对支付与额度规则。','通过账单和调用记录了解使用情况。'], note:'¥128.60 为示例余额，此预览不会创建订单或发起付款。'},
  records: {title:'使用记录', description:'按时间、模型与调用结果核对使用情况。', items:['查看模型名称与调用时间。','核对用量和费用，关注异常结果。','需要排查时查看调用详情。'], note:'当前显示的模型调用、时间与费用均为示例数据。'},
  playground: {title:'在线体验', description:'选择模型，开始一次对话或能力体验。', items:['先确认模型可用能力和对应计费。','在控制台查看调用记录。'], note:'当前设计预览不会向任何模型发起请求。'},
  profile: {title:'个人设置', description:'账户信息与界面偏好的入口。', items:['管理个人信息。','根据使用习惯调整界面偏好。'], note:'当前设计预览不会修改账户或主题设置。'}
};

function openPreview(key) {
  const content = previews[key];
  if (!content) return;
  document.getElementById('dialog-title').textContent = content.title;
  document.getElementById('dialog-description').textContent = content.description;
  document.getElementById('dialog-note').textContent = content.note;
  const list = document.getElementById('dialog-list');
  list.replaceChildren();
  for (const text of content.items) {
    const item = document.createElement('li');
    item.textContent = text;
    list.append(item);
  }
  dialog.showModal();
}

homeButton.addEventListener('click', () => showView('home'));
consoleButton.addEventListener('click', () => showView('console'));
search.addEventListener('input', filterModels);
document.getElementById('clear-filters').addEventListener('click', () => {
  activeFilter = 'all';
  search.value = '';
  filterModels();
  search.focus();
});
document.getElementById('close-dialog').addEventListener('click', () => dialog.close());
document.getElementById('dialog-confirm').addEventListener('click', () => dialog.close());
let copyTimer;
document.addEventListener('click', async event => {
  const filter = event.target.closest('[data-filter]');
  if (filter) { activeFilter = filter.dataset.filter; filterModels(); }
  const preview = event.target.closest('[data-dialog]');
  if (preview) openPreview(preview.dataset.dialog);
  if (event.target.closest('[data-overview]')) showView('console');
  const copy = event.target.closest('[data-copy]');
  if (copy) {
    const status = document.getElementById('copy-status');
    clearTimeout(copyTimer);
    try {
      await navigator.clipboard.writeText(copy.dataset.copy);
      status.textContent = '已复制示例地址 · 该地址不可实际调用';
    } catch {
      status.textContent = '无法自动复制，请长按或选中接口地址复制。';
    }
    status.classList.add('visible');
    copyTimer = setTimeout(() => status.classList.remove('visible'), 4500);
  }
  const link = event.target.closest('a');
  if (!link) return;
  const href = link.getAttribute('href');
  if (!['#home','#console','#models','#guide','#support'].includes(href)) return;
  event.preventDefault();
  if (href === '#guide' || href === '#support') openPreview(href.slice(1));
  else showView(href.slice(1));
});
document.addEventListener('keydown', event => {
  if (event.key !== 'Escape' || dialog.open) return;
  for (const menu of document.querySelectorAll('details[open]')) {
    menu.open = false;
    menu.querySelector('summary').focus();
  }
});
window.addEventListener('popstate', () => showView(['home','console','models'].includes(location.hash.slice(1)) ? location.hash.slice(1) : 'home', false));
const initialHash = location.hash.slice(1);
showView(['home','console','models'].includes(initialHash) ? initialHash : 'home', false);
