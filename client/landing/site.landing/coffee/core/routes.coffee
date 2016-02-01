kd    = require 'kd.js'
utils = require './utils'

do ->

  KODING = 'koding'

  handleRoot = (options)->

    # don't load the root content when we're just consuming a hash fragment
    return if location.hash.length

    { router } = kd.singletons
    { groupName, group, environment } = kd.config

    # root is home if group is koding
    if groupName is 'koding'
      return router.openSection 'Login', null, null, (app)->
        app.getView().animateToForm 'login'
        app.handleQuery options


    # if there is no such group take user to group creation with given group info
    if not group
      newUrl = "http://#{location.host.replace(groupName + '.', '')}/Teams?group=#{groupName}"
      return location.replace newUrl

    # if there is a group then take user to group login page
    else
      return router.openSection 'Team', null, null, (app) -> app.jumpTo 'login'

  handleInvitation = ({params : {token}, query}) ->

    utils.routeIfInvitationTokenIsValid token,
      success   : ({email}) ->
        utils.storeNewTeamData 'invitation', { token, email }
        kd.singletons.router.handleRoute '/Welcome'
      error     : ({responseText}) ->
        new kd.NotificationView title : responseText
        kd.singletons.router.handleRoute '/'


  handleTeamOnboardingRoute = (section, {params, query}) ->

    { groupName, group, environment } = kd.config
    { router }                        = kd.singletons

    # if group is koding or if the route doesnt have a subdomain we route to root.
    return router.handleRoute '/'  if groupName is KODING

    # if we dont have a group with the subdomain slug we again route to root.
    unless group
      newUrl = "http://#{location.host.replace(groupName + '.', '')}"
      return location.replace newUrl

    # if we dont have a valid email fetched from the invitation token we warn and route to root.
    unless utils.getTeamData().invitation?.email
      console.warn 'No valid invitation found!'
      return router.handleRoute '/'

    return handleTeamRoute section, {params, query}


  handleTeamRoute = (section, {params, query}) ->

    # we open the team creation or onboarding section
    return kd.singletons.router.openSection 'Team', null, null, (app) ->
      app.jumpTo section, params, query


  kd.registerRoutes 'Core',
    '/'                    : handleRoot
    ''                     : handleRoot
    # the routes below are subdomain routes
    # e.g. team.koding.com/Invitation
    '/Invitation/:token'   : handleInvitation
    '/Welcome'             : handleTeamOnboardingRoute.bind this, 'welcome'
    '/Join'                : handleTeamOnboardingRoute.bind this, 'join'
    '/Authenticate/:step?' : handleTeamOnboardingRoute.bind this, 'stacks'
    '/Congratz'            : handleTeamOnboardingRoute.bind this, 'congratz'
    '/Banned'              : handleTeamRoute.bind this, 'banned'