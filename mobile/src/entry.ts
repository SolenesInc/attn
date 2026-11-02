import { Runtime } from 'foldkit'
import { Model, init, subscriptions, update, view } from './main'

Runtime.run(
  Runtime.makeApplication({
    Model,
    init,
    update,
    view,
    subscriptions,
    container: document.getElementById('root'),
  }),
)
