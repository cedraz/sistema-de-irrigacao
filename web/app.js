// Site da irrigação. JS puro, sem framework, sem build.
'use strict';

const $ = (id) => document.getElementById(id);
const NOMES_DIAS = ['dom', 'seg', 'ter', 'qua', 'qui', 'sex', 'sáb'];

let estado = null;
let minutos = 10;
let editando = null;   // id da programação sendo editada, ou null = nova
let ocupado = false;

// ---------- conversa com a API ----------

async function api(rota, opcoes = {}) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 10000); // sem isso trava pra sempre
  try {
    const res = await fetch(rota, {
      ...opcoes,
      signal: ctrl.signal,
      headers: { 'Content-Type': 'application/json', ...(opcoes.headers || {}) },
    });
    const corpo = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(corpo.error || 'erro ' + res.status);
    return corpo;
  } finally {
    clearTimeout(timer);
  }
}

// ---------- estado da válvula ----------

// "4 min 05 s". A unidade vai escrita de propósito: "4:05" parece horário
// (quatro e cinco da manhã), e quem vai ler é o seu pai.
function duracao(seg) {
  seg = Math.max(0, Math.round(seg));
  const h = Math.floor(seg / 3600);
  const m = Math.floor((seg % 3600) / 60);
  const s = seg % 60;
  const dois = (n) => String(n).padStart(2, '0');
  if (h > 0) return `${h} h ${dois(m)} min ${dois(s)} s`;
  if (m > 0) return `${m} min ${dois(s)} s`;
  return `${s} s`;
}

function quandoTexto(iso) {
  const d = new Date(iso);
  const hoje = new Date();
  const amanha = new Date(); amanha.setDate(hoje.getDate() + 1);
  const hora = d.toTimeString().slice(0, 5);

  if (d.toDateString() === hoje.toDateString())   return `hoje às ${hora}`;
  if (d.toDateString() === amanha.toDateString()) return `amanhã às ${hora}`;
  return `${NOMES_DIAS[d.getDay()]} às ${hora}`;
}

function desenharEstado() {
  const botao = $('botao');
  const aviso = $('aviso');

  if (!estado) {
    $('rotulo').textContent = 'sem conexão';
    $('detalhe').textContent = '';
    botao.disabled = true;
    return;
  }

  // Aviso do ESP32: é a informação mais importante da tela. Sem ele a pessoa
  // aperta o botão, vê "aberta" e acha que tem água saindo.
  if (!estado.online) {
    aviso.textContent = 'O aparelho está desligado ou sem internet';
    aviso.className = 'aviso erro';
  } else {
    aviso.className = 'aviso escondido';
  }

  const aberta = estado.aberta;
  $('rotulo').textContent = aberta ? 'Regando' : 'Fechada';
  $('rotulo').style.color = aberta ? 'var(--verde)' : 'var(--texto)';

  if (aberta && estado.fecha_em) {
    const falta = (new Date(estado.fecha_em) - Date.now()) / 1000;
    $('detalhe').textContent = falta > 0 ? `fecha em ${duracao(falta)}` : 'fechando…';
  } else if (estado.proxima) {
    $('detalhe').textContent = `próxima rega ${quandoTexto(estado.proxima)}`;
  } else {
    $('detalhe').textContent = 'nenhum horário programado';
  }

  botao.textContent = ocupado ? '…' : (aberta ? 'PARAR' : 'REGAR AGORA');
  botao.className = aberta ? 'botao parar' : 'botao';
  botao.disabled = ocupado || !estado.online;
  $('duracao').className = aberta ? 'duracao escondido' : 'duracao';
}

async function puxarEstado() {
  const antes = estado && estado.aberta;
  try {
    estado = await api('/api/estado');
    if (estado.aberta !== antes) carregarHistorico();
  } catch {
    estado = null;   // mantém a tela honesta: não invento "fechada"
  }
  desenharEstado();
}

async function alternar() {
  if (!estado || ocupado) return;
  ocupado = true;
  desenharEstado();
  try {
    estado = await api('/api/valvula', {
      method: 'POST',
      body: JSON.stringify({ abrir: !estado.aberta, minutos }),
    });
  } catch (e) {
    $('aviso').textContent = e.message;
    $('aviso').className = 'aviso erro';
  } finally {
    ocupado = false;
    desenharEstado();
    carregarHistorico();
  }
}

// ---------- histórico ----------

// Sempre no horário de Brasília, mesmo que o celular esteja em outro fuso.
const FUSO = 'America/Sao_Paulo';
const fmtDia  = new Intl.DateTimeFormat('pt-BR', { timeZone: FUSO, weekday: 'short', day: '2-digit', month: '2-digit' });
const fmtData = new Intl.DateTimeFormat('pt-BR', { timeZone: FUSO, day: '2-digit', month: '2-digit' });
const fmtHora = new Intl.DateTimeFormat('pt-BR', { timeZone: FUSO, hour: '2-digit', minute: '2-digit', second: '2-digit' });

let historico = [];

async function carregarHistorico() {
  try {
    historico = await api('/api/historico?limite=30');
    desenharHistorico();
  } catch (e) {
    // Nunca falhar em silêncio: seção vazia sem explicação não diz nada a ninguém.
    $('historico').innerHTML = '';
    $('semHistorico').textContent = 'Não consegui carregar o histórico: ' + e.message;
    $('semHistorico').className = 'vazio';
    console.error('historico', e);
  }
}

function desenharHistorico() {
  const ul = $('historico');
  ul.innerHTML = '';
  $('semHistorico').className = historico.length ? 'vazio escondido' : 'vazio';

  for (const r of historico) {
    const inicio = new Date(r.inicio);
    const fim = r.fim ? new Date(r.fim) : null;
    const segundos = ((fim || new Date()) - inicio) / 1000;

    // Rega que passou da meia-noite: mostra a data do fechamento também.
    let fechou = '<span class="agora">aberta agora</span>';
    if (fim) {
      const outroDia = fmtData.format(inicio) !== fmtData.format(fim);
      fechou = 'Fechou ' + (outroDia ? fmtData.format(fim) + ' ' : '') + fmtHora.format(fim);
    }

    const li = document.createElement('li');
    li.innerHTML =
      `<div class="topo"><span class="dia">${fmtDia.format(inicio)}</span>` +
      `<span class="origem">${r.origem === 'agenda' ? 'automático' : 'manual'}</span></div>` +
      `<div class="horas">Abriu ${fmtHora.format(inicio)} → ${fechou}</div>` +
      `<div class="dur">${fim ? 'Ficou aberta' : 'Aberta há'} <b>${duracao(segundos)}</b></div>`;

    // O aviso só vale pra rega já fechada: a aberta ainda está esperando a
    // resposta do aparelho, que chega em milissegundos.
    if (fim && r.confirmada === false) {
      li.insertAdjacentHTML('beforeend',
        '<div class="alerta">⚠ O aparelho não respondeu — a água pode não ter saído.</div>');
    }
    if (r.estimado) {
      li.insertAdjacentHTML('beforeend',
        '<div class="nota">Horário de fechamento estimado: o servidor reiniciou durante a rega.</div>');
    }
    ul.appendChild(li);
  }
}

// ---------- horários ----------

function textoDias(dias) {
  if (dias.length === 7) return 'todos os dias';
  if (dias.length === 5 && [1,2,3,4,5].every(d => dias.includes(d))) return 'de segunda a sexta';
  if (dias.length === 2 && dias.includes(0) && dias.includes(6)) return 'fim de semana';
  return [...dias].sort().map(d => NOMES_DIAS[d]).join(', ');
}

async function carregarProgramacoes() {
  let lista;
  try {
    lista = await api('/api/programacoes');
  } catch {
    return;
  }

  const ul = $('lista');
  ul.innerHTML = '';
  $('vazio').className = lista.length ? 'vazio escondido' : 'vazio';

  for (const p of lista) {
    const li = document.createElement('li');
    if (!p.ativo) li.className = 'desativado';

    const quando = document.createElement('div');
    quando.className = 'quando';
    quando.innerHTML =
      `<div class="hora">${p.hora} · ${p.duracao} min</div>` +
      `<div class="dias">${textoDias(p.dias)}</div>`;
    quando.onclick = () => abrirForm(p);

    const chave = document.createElement('button');
    chave.className = p.ativo ? 'interruptor ligado' : 'interruptor';
    chave.setAttribute('aria-label', p.ativo ? 'desligar horário' : 'ligar horário');
    chave.onclick = async () => {
      await api('/api/programacoes/' + p.id, {
        method: 'PUT',
        body: JSON.stringify({ ...p, ativo: !p.ativo }),
      });
      carregarProgramacoes();
      puxarEstado();
    };

    const lixo = document.createElement('button');
    lixo.className = 'apagar';
    lixo.textContent = '✕';
    lixo.setAttribute('aria-label', 'apagar horário');
    lixo.onclick = async () => {
      if (!confirm(`Apagar o horário das ${p.hora}?`)) return;
      await api('/api/programacoes/' + p.id, { method: 'DELETE' });
      carregarProgramacoes();
      puxarEstado();
    };

    li.append(quando, chave, lixo);
    ul.appendChild(li);
  }
}

// ---------- formulário ----------

function diasSelecionados() {
  return [...document.querySelectorAll('#dias button.on')].map(b => +b.dataset.dia);
}

function abrirForm(p) {
  editando = p ? p.id : null;
  $('tituloForm').textContent = p ? 'Alterar horário' : 'Novo horário';
  $('hora').value = p ? p.hora : '07:00';
  $('dur').value = p ? p.duracao : 10;

  document.querySelectorAll('#dias button').forEach(b => {
    const marcado = p ? p.dias.includes(+b.dataset.dia) : true;
    b.className = marcado ? 'on' : '';
  });

  $('modal').className = 'modal';
}

function fecharForm() {
  $('modal').className = 'modal escondido';
  editando = null;
}

async function salvarForm(ev) {
  ev.preventDefault();
  const dias = diasSelecionados();
  if (dias.length === 0) {
    alert('Escolha pelo menos um dia da semana.');
    return;
  }

  const corpo = {
    hora: $('hora').value,
    duracao: +$('dur').value,
    dias,
    ativo: true,
  };

  try {
    if (editando) {
      await api('/api/programacoes/' + editando, { method: 'PUT', body: JSON.stringify(corpo) });
    } else {
      await api('/api/programacoes', { method: 'POST', body: JSON.stringify(corpo) });
    }
    fecharForm();
    carregarProgramacoes();
    puxarEstado();
  } catch (e) {
    alert(e.message);
  }
}

// ---------- ligações ----------

$('botao').onclick = alternar;
$('novo').onclick = () => abrirForm(null);
$('cancelar').onclick = fecharForm;
$('form').onsubmit = salvarForm;

$('todos').onclick = () => {
  document.querySelectorAll('#dias button').forEach(b => { b.className = 'on'; });
};

document.querySelectorAll('#dias button').forEach(b => {
  b.onclick = () => { b.className = b.className === 'on' ? '' : 'on'; };
});

document.querySelectorAll('.min').forEach(b => {
  b.onclick = () => {
    minutos = +b.dataset.min;
    document.querySelectorAll('.min').forEach(o => { o.className = 'min'; });
    b.className = 'min selecionado';
  };
});

$('modal').onclick = (e) => { if (e.target === $('modal')) fecharForm(); };

puxarEstado();
carregarProgramacoes();
carregarHistorico();
setInterval(puxarEstado, 3000);

// A cada segundo, só redesenha com o que já tem — não pergunta nada pro
// servidor. É isso que faz os segundos andarem de um em um.
setInterval(() => {
  desenharEstado();
  if (historico.some((r) => !r.fim)) desenharHistorico();
}, 1000);
