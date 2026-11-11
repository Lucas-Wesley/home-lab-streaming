import { createApp } from 'vue'
import App from './App.vue'
import './style.css'

createApp(App).mount('#app')

// App novo no servidor: o celular deixa o PWA aberto por dias, e o código velho falando com a API nova
// dá erro. Ao voltar para o app, procura a versão nova; quando ela assume, recarrega (menos no player,
// para não cortar o vídeo; ele pega a nova na próxima vez).
if ('serviceWorker' in navigator) {
  const tinha = !!navigator.serviceWorker.controller
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (tinha && !location.hash.startsWith('#/assistir/')) location.reload()
  })
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') navigator.serviceWorker.getRegistration().then((r) => r?.update()).catch(() => {})
  })
}
