<script setup>
import { computed, inject, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from './api.js'
import { sincroniaLegivel } from './legendas.js'

// Lista as legendas do OpenSubtitles para um vídeo (S21), um idioma por aba, a mais provável no
// topo, e oferece gerar uma pelo áudio (S29). A escolhida fica guardada no servidor e vale no
// player e nas telas. liberar: aberta pelo controle de uma tela, que pode parar uns segundos para
// o servidor puxar o áudio da assinatura.
const props = defineProps({ chave: { type: String, required: true }, liberar: { type: Boolean, default: false } })
const emit = defineEmits(['escolheu', 'ajustou'])
const recado = inject('recado')

const dados = ref(null)
const erro = ref('')
const escolhendo = ref('')
const verTodas = ref({})

// Cada idioma com um selo de cor própria: dá para distinguir de longe, sem ler o nome do arquivo.
const IDIOMAS = {
  'pt-BR': { nome: 'Português (Brasil)', selo: 'PT-BR', classe: 'br' },
  'pt-PT': { nome: 'Português (Portugal)', selo: 'PT', classe: 'pt' },
  en: { nome: 'Inglês', selo: 'EN', classe: 'en' },
  pt: { nome: 'Português', selo: 'PT', classe: 'br' },
  es: { nome: 'Espanhol', selo: 'ES', classe: '' },
}
const POUCAS = 5

const grupos = computed(() =>
  Object.keys(IDIOMAS)
    .filter((idioma) => ['pt-BR', 'pt-PT', 'en'].includes(idioma))
    .map((idioma) => ({ idioma, opcoes: (dados.value?.opcoes || []).filter((o) => o.idioma === idioma) }))
    .filter((g) => g.opcoes.length),
)

// Um idioma por vez, em abas: começa no da legenda em uso, ou no primeiro que tiver.
const aba = ref('')
const abaAtual = computed(() => (grupos.value.some((g) => g.idioma === aba.value) ? aba.value : dados.value?.escolhida?.idioma && grupos.value.some((g) => g.idioma === dados.value.escolhida.idioma) ? dados.value.escolhida.idioma : grupos.value[0]?.idioma))
const grupoAtual = computed(() => grupos.value.find((g) => g.idioma === abaAtual.value))
const idiomaDe = (codigo) => IDIOMAS[codigo] || { nome: codigo, selo: (codigo || '?').toUpperCase(), classe: '' }

// Ajuste manual da sincronia: soma ao atraso guardado (automático ou não).
const ajustando = ref(false)
async function ajustar(delta) {
  const l = dados.value?.escolhida
  if (!l) return
  ajustando.value = true
  try {
    const nova = await api(`/api/legendas/${props.chave}`, { metodo: 'PATCH', corpo: { atraso: delta === null ? 0 : (l.atraso || 0) + delta } })
    dados.value.escolhida = nova
    emit('ajustou', nova)
  } catch (e) {
    recado(e.message, true)
  } finally {
    ajustando.value = false
  }
}

// Gerar pelo áudio (S29): o Parakeet ouve o vídeo minuto a minuto, à frente de quem assiste.
const gerada = computed(() => dados.value?.escolhida?.geracao || null)
const gerando = computed(() => gerada.value && !['pronta', 'parada', 'erro'].includes(gerada.value.estado))
const faixas = computed(() => dados.value?.gerar?.faixas || [])
const nomeDoIdioma = (codigo) => (codigo ? idiomaDe(codigo).nome : 'idioma não informado')
async function gerar(faixa) {
  escolhendo.value = 'gerar'
  try {
    const corpo = { liberar: props.liberar }
    if (faixa !== undefined) corpo.faixa = faixa
    const legenda = await api(`/api/legendas/${props.chave}/gerar`, { metodo: 'POST', corpo })
    emit('escolheu', legenda)
  } catch (e) {
    recado(e.message, true)
  } finally {
    escolhendo.value = ''
  }
}
async function pararGeracao() {
  await api(`/api/legendas/${props.chave}/gerar`, { metodo: 'DELETE' }).catch(() => {})
  dados.value.escolhida = (await api(`/api/legendas/${props.chave}`)).escolhida
}
function minutos(s) {
  const m = Math.floor((s || 0) / 60)
  return `${Math.floor(m / 60) ? `${Math.floor(m / 60)}h` : ''}${String(m % 60).padStart(Math.floor(m / 60) ? 2 : 1, '0')}min`
}

async function escolher(o) {
  escolhendo.value = o ? o.id : 'nenhuma'
  try {
    const legenda = o
      ? await api(`/api/legendas/${props.chave}`, { metodo: 'PUT', corpo: { id: o.id, idioma: o.idioma, nome: o.nome, exata: !!o.exata }, tempo: 40000 })
      : await api(`/api/legendas/${props.chave}`, { metodo: 'DELETE' })
    emit('escolheu', legenda || null)
  } catch (e) {
    recado(e.message, true)
  } finally {
    escolhendo.value = ''
  }
}

// Enquanto gera, o andamento se atualiza sozinho.
let relogio
onMounted(async () => {
  try {
    dados.value = await api(`/api/legendas/${props.chave}?buscar`, { tempo: 40000 })
  } catch (e) {
    erro.value = e.message
  }
  relogio = setInterval(async () => {
    if (!gerando.value) return
    try {
      dados.value.escolhida = (await api(`/api/legendas/${props.chave}`)).escolhida
    } catch {
      // tenta de novo
    }
  }, 4000)
})
onBeforeUnmount(() => clearInterval(relogio))
</script>

<template>
  <div class="legendas">
    <div v-if="erro" class="aviso erro">{{ erro }}</div>
    <p v-else-if="!dados" class="vazio">Procurando legendas…</p>
    <p v-else-if="!dados.configurado && !dados.gerar?.disponivel" class="aviso">Para buscar legendas, conecte o OpenSubtitles em <a href="#/ajustes/legendas">Ajustes</a>.</p>

    <!-- Em uso: o idioma em destaque, a sincronia e o ajuste -->
    <div v-if="dados?.configurado || dados?.gerar?.disponivel" class="em-uso">
      <template v-if="dados.escolhida">
        <div class="cabeca-em-uso">
          <span class="selo grande" :class="idiomaDe(dados.escolhida.idioma).classe">{{ idiomaDe(dados.escolhida.idioma).selo }}</span>
          <span class="texto-em-uso">
            <span class="rotulo-pequeno">Legenda em uso</span>
            <strong class="idioma-em-uso">{{ idiomaDe(dados.escolhida.idioma).nome }}</strong>
            <span class="arquivo">{{ dados.escolhida.nome }}</span>
          </span>
          <button class="botao tirar" :disabled="!!escolhendo" @click="escolher(null)">Tirar</button>
        </div>
        <div v-if="gerada" class="geracao">
          <div class="barra-da-geracao" :style="{ '--feito': `${gerada.total ? (100 * gerada.feitos) / gerada.total : 0}%` }"></div>
          <p>
            <strong v-if="gerada.estado === 'pronta'">Legenda completa.</strong>
            <strong v-else-if="gerada.estado === 'erro'">Parou com erro.</strong>
            <strong v-else-if="gerada.estado === 'parada'">Geração parada.</strong>
            <strong v-else-if="gerada.estado === 'esperando'">Esperando a assinatura.</strong>
            <strong v-else>Gerando pelo áudio…</strong>
            <template v-if="gerada.total"> {{ gerada.feitos }} de {{ gerada.total }} min ouvidos<template v-if="gerada.revisados">, {{ gerada.revisados }} revisados</template>.</template>
            <template v-if="gerando && gerada.ate"> Pronta até {{ minutos(gerada.ate) }} a partir de onde você está.</template>
          </p>
          <p v-if="gerada.mensagem" class="suave">{{ gerada.mensagem }}</p>
          <div class="linha-acoes">
            <button v-if="gerando" class="botao" @click="pararGeracao">Parar</button>
            <button v-else-if="gerada.estado !== 'pronta'" class="botao" :disabled="!!escolhendo" @click="gerar()">Continuar</button>
          </div>
        </div>
        <p v-if="!gerada" class="estado-da-sincronia">
          <template v-if="dados.escolhida.alinhando">Conferindo a sincronia…</template>
          <template v-else><strong>Sincronia:</strong> {{ sincroniaLegivel(dados.escolhida) }}. <span class="suave">{{ dados.escolhida.ajuste }}</span></template>
        </p>
        <div v-if="!gerada" class="ajuste">
          <button class="botao" :disabled="ajustando" title="A legenda aparece depois da fala" @click="ajustar(-0.5)">−0,5 s<small>legenda atrasada</small></button>
          <button class="botao" :disabled="ajustando" title="A legenda aparece antes da fala" @click="ajustar(0.5)">+0,5 s<small>legenda adiantada</small></button>
          <button v-if="dados.escolhida.atraso" class="botao" :disabled="ajustando" @click="ajustar(null)">Zerar</button>
        </div>
      </template>
      <p v-else class="sem-legenda"><span class="selo grande">—</span><span><span class="rotulo-pequeno">Legenda em uso</span><strong class="idioma-em-uso">Sem legenda</strong></span></p>
    </div>

    <!-- Gerar pelo áudio -->
    <section v-if="dados?.gerar?.disponivel && !gerada" class="gerar">
      <div class="cabeca-gerar">
        <span class="selo grande ia">IA</span>
        <span>
          <strong>Gerar pelo áudio</strong>
          <span class="suave">Transcreve as falas no idioma do áudio. Fica pronta em menos de 1 minuto e continua à frente enquanto você assiste<template v-if="dados.gerar.revisao">, com uma segunda revisão (Whisper + Claude)</template>.</span>
        </span>
      </div>
      <div class="linha-acoes">
        <template v-if="faixas.length > 1">
          <button v-for="f in faixas" :key="f.indice" class="botao" :disabled="!!escolhendo" @click="gerar(f.indice)">
            {{ escolhendo === 'gerar' ? 'Pedindo…' : `Áudio em ${nomeDoIdioma(f.idioma).toLowerCase()}` }}
          </button>
        </template>
        <button v-else class="botao principal" :disabled="!!escolhendo" @click="gerar()">{{ escolhendo === 'gerar' ? 'Pedindo…' : 'Gerar legenda' }}</button>
      </div>
    </section>

    <template v-if="dados?.configurado">
      <p v-if="!grupos.length" class="vazio">Nenhuma legenda em português ou inglês para este vídeo.</p>
      <div v-else class="abas" role="tablist">
        <button
          v-for="g in grupos"
          :key="g.idioma"
          class="aba"
          :class="{ ativa: abaAtual === g.idioma }"
          role="tab"
          :aria-selected="abaAtual === g.idioma"
          @click="aba = g.idioma"
        >
          <span class="selo" :class="idiomaDe(g.idioma).classe">{{ idiomaDe(g.idioma).selo }}</span>
          {{ idiomaDe(g.idioma).nome }}
          <span class="quantas">{{ g.opcoes.length }}</span>
        </button>
      </div>

      <ul v-if="grupoAtual" class="opcoes">
        <li v-for="o in verTodas[grupoAtual.idioma] ? grupoAtual.opcoes : grupoAtual.opcoes.slice(0, POUCAS)" :key="o.id">
          <button class="opcao" :class="{ escolhida: dados.escolhida?.id === o.id }" :disabled="!!escolhendo" @click="escolher(o)">
            <span class="selo" :class="idiomaDe(o.idioma).classe">{{ idiomaDe(o.idioma).selo }}</span>
            <span class="texto">
              <span class="nome">{{ escolhendo === o.id ? 'Baixando…' : o.nome }}</span>
              <span class="detalhes">
                <span v-if="dados.escolhida?.id === o.id" class="chip ok">em uso</span>
                <span v-if="o.exata" class="chip exata">✓ feita para este arquivo</span>
                <span v-if="o.maquina" class="chip">tradução automática</span>
                <span v-if="o.surdos" class="chip">descreve sons</span>
                <span>{{ o.baixadas.toLocaleString('pt-BR') }} downloads</span>
              </span>
            </span>
          </button>
        </li>
      </ul>
      <button v-if="grupoAtual && !verTodas[grupoAtual.idioma] && grupoAtual.opcoes.length > POUCAS" class="mais" @click="verTodas[grupoAtual.idioma] = true">
        Mostrar mais {{ grupoAtual.opcoes.length - POUCAS }} em {{ idiomaDe(grupoAtual.idioma).nome.toLowerCase() }}
      </button>
      <p class="suave nota">As primeiras de cada idioma são as da mesma versão do vídeo. Se ficar fora de tempo, ajuste acima ou escolha outra.</p>
    </template>
  </div>
</template>

<style scoped>
.em-uso {
  margin-bottom: 18px;
  padding: 14px 16px;
  border-radius: var(--raio);
  background: var(--superficie);
}

.cabeca-em-uso,
.sem-legenda {
  display: flex;
  gap: 12px;
  align-items: center;
  margin: 0;
}

.texto-em-uso,
.sem-legenda > span:last-child {
  display: grid;
  flex: 1;
  gap: 1px;
  min-width: 0;
}

.rotulo-pequeno {
  color: var(--texto-suave);
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.idioma-em-uso {
  font-size: 1.1rem;
}

.arquivo {
  overflow: hidden;
  color: var(--texto-suave);
  font-size: 0.8rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tirar {
  flex: none;
}

.geracao {
  display: grid;
  gap: 8px;
  margin-top: 12px;
  font-size: 0.9rem;
  line-height: 1.45;
}

.geracao p {
  margin: 0;
}

.barra-da-geracao {
  height: 6px;
  border-radius: 999px;
  background: linear-gradient(to right, var(--marca) var(--feito), var(--superficie-forte) var(--feito));
}

.gerar {
  display: grid;
  gap: 12px;
  margin-bottom: 18px;
  padding: 14px 16px;
  border: 1px dashed var(--linha-forte);
  border-radius: var(--raio);
}

.cabeca-gerar {
  display: flex;
  gap: 12px;
  align-items: flex-start;
}

.cabeca-gerar > span:last-child {
  display: grid;
  gap: 2px;
  font-size: 0.88rem;
  line-height: 1.45;
}

.cabeca-gerar strong {
  font-size: 1rem;
}

.selo.ia {
  background: var(--marca);
  color: var(--marca-texto);
}

.estado-da-sincronia {
  margin: 12px 0 10px;
  font-size: 0.9rem;
  line-height: 1.45;
}

.ajuste {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.ajuste .botao {
  display: grid;
  gap: 1px;
  min-width: 112px;
  padding-block: 6px;
  font-size: 1rem;
  font-weight: 600;
  line-height: 1.2;
}

.ajuste small {
  color: var(--texto-suave);
  font-size: 0.7rem;
  font-weight: 500;
}

/* Selo do idioma */
.selo {
  display: inline-grid;
  flex: none;
  place-items: center;
  min-width: 34px;
  height: 22px;
  padding: 0 6px;
  border-radius: 6px;
  background: var(--superficie-forte);
  color: var(--texto-suave);
  font-size: 0.7rem;
  font-weight: 750;
  letter-spacing: 0.03em;
}

.selo.grande {
  min-width: 52px;
  height: 40px;
  border-radius: 10px;
  font-size: 0.9rem;
}

/* Cores fixas (claro e escuro), fortes o bastante para o texto branco. */
.selo.br {
  background: #15803d;
  color: #fff;
}

.selo.pt {
  background: #0f766e;
  color: #fff;
}

.selo.en {
  background: #2459c7;
  color: #fff;
}

.abas {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 4px;
}

.aba {
  display: inline-flex;
  flex: none;
  gap: 8px;
  align-items: center;
  padding: 8px 12px 8px 8px;
  border: 1px solid var(--linha-forte);
  border-radius: 999px;
  background: none;
  color: var(--texto);
  font: inherit;
  font-size: 0.9rem;
  font-weight: 550;
  cursor: pointer;
}

.aba.ativa {
  border-color: var(--texto);
  background: var(--superficie-forte);
}

.quantas {
  color: var(--texto-suave);
  font-size: 0.8rem;
  font-weight: 500;
}

.opcoes {
  margin: 0;
  padding: 0;
  list-style: none;
}

.opcao {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  width: 100%;
  padding: 12px 8px;
  border: 0;
  border-bottom: 1px solid var(--linha);
  border-radius: 0;
  background: none;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.opcao .selo {
  margin-top: 1px;
}

.opcao.escolhida {
  border-radius: var(--raio-p);
  background: var(--ok-fundo);
}

.opcao:hover .nome {
  color: var(--marca);
}

.texto {
  display: grid;
  gap: 6px;
  min-width: 0;
}

.nome {
  overflow-wrap: anywhere;
  font-size: 0.95rem;
  font-weight: 550;
  line-height: 1.35;
}

.detalhes {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  color: var(--texto-suave);
  font-size: 0.8rem;
}

.chip.exata {
  background: color-mix(in srgb, var(--verde) 18%, transparent);
  color: var(--verde);
}

.mais {
  margin: 10px 0 0;
  padding: 0;
  border: 0;
  background: none;
  color: var(--marca);
  font: inherit;
  font-size: 0.88rem;
  cursor: pointer;
}

.nota {
  margin: 16px 0 0;
  font-size: 0.8rem;
  line-height: 1.45;
}
</style>
