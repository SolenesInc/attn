import { Runtime } from 'foldkit'
import { mark } from './metrics'
import { Model, init, subscriptions, update, view } from './main'

mark('entry_eval')

const application = Runtime.makeApplication({
  Model,
  init,
  update,
  view,
  subscriptions,
  container: document.getElementById('root'),
})

Runtime.run(application)
mark('runtime_started')
