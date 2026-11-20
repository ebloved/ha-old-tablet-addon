(function () {
  'use strict';

  var cfg = null;
  var states = [];

  function $(id) {
    return document.getElementById(id);
  }

  function req(method, path, body) {
    var opts = { method: method, headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function (r) {
      if (!r.ok) {
        throw new Error(path + ': ' + r.status);
      }
      return r.json();
    });
  }

  function load() {
    return Promise.all([
      req('GET', 'api/admin/config'),
      req('GET', 'api/admin/status'),
      req('GET', 'api/admin/states')
    ])
      .then(function (res) {
        cfg = res[0];
        states = res[2] || [];
        if (!$('host').value) {
          $('host').value = location.hostname || 'homeassistant.local';
        }
        renderStatus(res[1]);
        renderTablet();
        renderWidgets();
      })
      .catch(function (e) {
        $('ha-status').textContent = 'ошибка: ' + e.message;
      });
  }

  function renderStatus(st) {
    $('ha-dot').className = 'dot' + (st.connected ? ' on' : '');
    $('ha-status').textContent = st.connected ? 'Home Assistant подключён' : 'нет связи с Home Assistant';
    $('ha-entities').textContent = st.connected ? ('сущностей: ' + st.entities) : '';
    $('cfg-version').textContent = 'конфиг v' + st.version;
  }

  function tabletHost() {
    var h = ($('host').value || '')
      .replace(/^https?:\/\//, '')
      .replace(/\/.*$/, '')
      .replace(/:.*$/, '')
      .trim();
    return h || 'homeassistant.local';
  }

  function renderTablet() {
    var host = tabletHost();
    $('token').textContent = cfg.device_token || '—';
    if (!cfg.device_token) {
      $('tablet-url').textContent = '—';
      $('qr').removeAttribute('src');
      return;
    }
    $('tablet-url').textContent = 'http://' + host + ':8088/?t=' + cfg.device_token;
    $('qr').src =
      'api/admin/qr?host=' +
      encodeURIComponent(host) +
      '&v=' +
      encodeURIComponent(cfg.device_token.slice(0, 8));
  }

  function makeRow(w, index) {
    var tr = document.createElement('tr');
    tr.innerHTML =
      '<td><select class="f-type">' +
      '<option value="entity">entity</option>' +
      '<option value="sensor">sensor</option>' +
      '<option value="clock">clock</option>' +
      '<option value="rss">rss</option>' +
      '</select></td>' +
      '<td><input class="f-entity" list="entity-list" placeholder="light.kitchen"></td>' +
      '<td><input class="f-name" placeholder="Kitchen"></td>' +
      '<td><input class="f-feed" placeholder="https://example.com/feed.xml"></td>' +
      '<td class="num"><input class="f-limit" type="number" min="3" max="30"></td>' +
      '<td class="num"><input class="f-w" type="number" min="1" max="6"></td>' +
      '<td class="num"><input class="f-h" type="number" min="1" max="4"></td>' +
      '<td class="actions">' +
      '<button class="up" title="выше">↑</button>' +
      '<button class="down" title="ниже">↓</button>' +
      '<button class="del" title="удалить">×</button>' +
      '</td>';

    tr.querySelector('.f-type').value = w.type;
    tr.querySelector('.f-entity').value = w.entity || '';
    tr.querySelector('.f-name').value = w.name || '';
    tr.querySelector('.f-feed').value = w.feed || '';
    tr.querySelector('.f-limit').value = w.limit || 8;
    tr.querySelector('.f-w').value = w.w || 1;
    tr.querySelector('.f-h').value = w.h || 1;

    tr.querySelector('.f-type').addEventListener('change', function () {
      applyType(tr);
    });
    tr.querySelector('.up').addEventListener('click', function () {
      move(index, -1);
    });
    tr.querySelector('.down').addEventListener('click', function () {
      move(index, 1);
    });
    tr.querySelector('.del').addEventListener('click', function () {
      cfg.widgets.splice(index, 1);
      renderWidgets();
    });

    applyType(tr);
    return tr;
  }

  function applyType(tr) {
    var type = tr.querySelector('.f-type').value;
    tr.querySelector('.f-entity').disabled = type === 'rss' || type === 'clock';
    tr.querySelector('.f-feed').disabled = type !== 'rss';
    tr.querySelector('.f-limit').disabled = type !== 'rss';
  }

  function move(index, delta) {
    var to = index + delta;
    if (to < 0 || to >= cfg.widgets.length) {
      return;
    }
    var tmp = cfg.widgets[index];
    cfg.widgets[index] = cfg.widgets[to];
    cfg.widgets[to] = tmp;
    renderWidgets();
  }

  function renderWidgets() {
    var tbody = $('widgets');
    tbody.innerHTML = '';

    var dl = $('entity-list');
    dl.innerHTML = '';
    for (var i = 0; i < states.length; i++) {
      var opt = document.createElement('option');
      opt.value = states[i].entity_id;
      opt.label = states[i].name + ' — ' + states[i].state;
      dl.appendChild(opt);
    }

    for (var j = 0; j < cfg.widgets.length; j++) {
      tbody.appendChild(makeRow(cfg.widgets[j], j));
    }

    $('title').value = cfg.title || '';
  }

  function collect() {
    var tbody = $('widgets');
    var rows = tbody.querySelectorAll('tr');
    var out = [];
    for (var i = 0; i < rows.length; i++) {
      var tr = rows[i];
      out.push({
        id: (cfg.widgets[i] && cfg.widgets[i].id) || '',
        type: tr.querySelector('.f-type').value,
        entity: tr.querySelector('.f-entity').value.trim(),
        name: tr.querySelector('.f-name').value.trim(),
        feed: tr.querySelector('.f-feed').value.trim(),
        limit: parseInt(tr.querySelector('.f-limit').value, 10) || 8,
        w: parseInt(tr.querySelector('.f-w').value, 10) || 1,
        h: parseInt(tr.querySelector('.f-h').value, 10) || 1
      });
    }
    return out;
  }

  function save() {
    var body = {
      title: $('title').value.trim(),
      widgets: collect()
    };
    $('save-status').textContent = 'сохранение…';
    req('PUT', 'api/admin/config', body)
      .then(function (saved) {
        cfg = saved;
        $('save-status').textContent = 'сохранено, планшет обновится сам';
        renderTablet();
        renderWidgets();
        setTimeout(function () {
          $('save-status').textContent = '';
        }, 3000);
        return req('GET', 'api/admin/status');
      })
      .then(renderStatus)
      .catch(function (e) {
        $('save-status').textContent = 'ошибка: ' + e.message;
      });
  }

  function add() {
    cfg.widgets.push({
      id: '',
      type: 'entity',
      entity: '',
      name: '',
      w: 1,
      h: 1
    });
    renderWidgets();
  }

  function regen() {
    if (!confirm('Старый токен перестанет работать. Планшет придётся открыть по новой ссылке. Продолжить?')) {
      return;
    }
    req('POST', 'api/admin/token')
      .then(function (res) {
        cfg.device_token = res.token;
        renderTablet();
      })
      .catch(function (e) {
        alert('Не удалось: ' + e.message);
      });
  }

  $('save').addEventListener('click', save);
  $('add').addEventListener('click', add);
  $('regen').addEventListener('click', regen);
  $('host').addEventListener('change', renderTablet);

  load();
  setInterval(function () {
    req('GET', 'api/admin/status').then(renderStatus).catch(function () {});
  }, 10000);
})();
